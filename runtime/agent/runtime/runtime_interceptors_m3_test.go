package runtime

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/runtime/agent/session"
	sessioninmem "github.com/CaliLuke/loom-mcp/runtime/agent/session/inmem"
	"github.com/stretchr/testify/require"
)

type interceptableModelClient struct {
	calls *[]string
}

func (c interceptableModelClient) Complete(_ context.Context, req *model.Request) (*model.Response, error) {
	*c.calls = append(*c.calls, "model")
	return &model.Response{
		Content: []model.Message{{
			Role: model.ConversationRoleAssistant,
			Parts: []model.Part{
				model.TextPart{Text: "model:" + req.Model},
			},
		}},
	}, nil
}

func (c interceptableModelClient) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return emptyModelStream{}, nil
}

type emptyModelStream struct{}

func (emptyModelStream) Recv() (model.Chunk, error) { return model.Chunk{}, io.EOF }
func (emptyModelStream) Close() error               { return nil }
func (emptyModelStream) Metadata() map[string]any   { return nil }

func TestModelInterceptorsMutateRequestAndResponseOnce(t *testing.T) {
	var calls []string
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		BeforeModelFunc: func(_ context.Context, input *BeforeModelInput) (*BeforeModelDecision, error) {
			calls = append(calls, "before-model")
			require.Equal(t, agent.Ident("svc.agent"), input.AgentID)
			req := *input.Request
			req.Model = "rewritten"
			return &BeforeModelDecision{Request: &req}, nil
		},
		AfterModelFunc: func(_ context.Context, input *AfterModelInput) (*AfterModelDecision, error) {
			calls = append(calls, "after-model")
			require.NoError(t, input.Err)
			input.Response.Content[0].Parts = []model.Part{model.TextPart{Text: "after"}}
			return &AfterModelDecision{Response: input.Response}, nil
		},
	}))
	rt.models["default"] = interceptableModelClient{calls: &calls}

	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		events:  nil,
		cache:   CachePolicy{},
		turnID:  "turn-1",
		labels:  map[string]string{"tenant": "acme"},
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	resp, err := client.Complete(context.Background(), &model.Request{Model: "original"})
	require.NoError(t, err)
	require.Equal(t, []string{"before-model", "model", "after-model"}, calls)
	require.Equal(t, "after", resp.Content[0].Parts[0].(model.TextPart).Text)
}

func TestEventInterceptorCanDropBeforeRunlogStreamAndBus(t *testing.T) {
	rl := &recordingRunlog{}
	bus := hooks.NewBus()
	store := sessioninmem.New()
	rt := New(
		WithRunEventStore(rl),
		WithHooks(bus),
		WithSessionStore(store),
		WithInterceptors(RuntimeInterceptorFuncs{
			BeforeEventFunc: func(_ context.Context, input *BeforeEventInput) (*BeforeEventDecision, error) {
				require.Equal(t, hooks.PlannerNote, input.Event.Type())
				return &BeforeEventDecision{Drop: true}, nil
			},
		}),
	)

	var published hooks.Event
	sub, err := bus.Register(hooks.SubscriberFunc(func(_ context.Context, evt hooks.Event) error {
		published = evt
		return nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() })

	now := time.Now().UTC()
	_, err = store.CreateSession(context.Background(), "sess-1", now)
	require.NoError(t, err)
	require.NoError(t, store.UpsertRun(context.Background(), session.RunMeta{
		AgentID:   "svc.agent",
		RunID:     "run-1",
		SessionID: "sess-1",
		Status:    session.RunStatusPending,
		StartedAt: now,
		UpdatedAt: now,
	}))

	input, err := hooks.EncodeToHookInput(hooks.NewPlannerNoteEvent("run-1", "svc.agent", "sess-1", "note", nil), "turn-1")
	require.NoError(t, err)
	require.NoError(t, rt.hookActivity(context.Background(), input))
	require.Empty(t, rl.events)
	require.Nil(t, published)
}

func TestRunInterceptorsOrderAndErrorShortCircuit(t *testing.T) {
	var calls []string
	runErr := errors.New("blocked")
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		BeforeRunFunc: func(_ context.Context, input *BeforeRunInput) (*BeforeRunDecision, error) {
			calls = append(calls, "before")
			require.Equal(t, agent.Ident("svc.agent"), input.AgentID)
			return nil, runErr
		},
		AfterRunFunc: func(context.Context, *AfterRunInput) (*AfterRunDecision, error) {
			calls = append(calls, "after")
			return nil, nil
		},
	}))
	_, err := runBeforeRunInterceptors(context.Background(), rt.interceptors, RunInput{AgentID: "svc.agent", RunID: "run-1"}, run.Context{RunID: "run-1"})
	require.ErrorIs(t, err, runErr)
	require.Equal(t, []string{"before"}, calls)
}

var _ runlog.Store = (*recordingRunlog)(nil)
