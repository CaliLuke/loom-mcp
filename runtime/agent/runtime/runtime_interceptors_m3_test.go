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
	"github.com/CaliLuke/loom-mcp/runtime/agent/stream"
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

type closeCountingModelClient struct {
	stream *closeCountingModelStream
}

func (c closeCountingModelClient) Complete(context.Context, *model.Request) (*model.Response, error) {
	return nil, errors.New("complete unsupported")
}

func (c closeCountingModelClient) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return c.stream, nil
}

type closeCountingModelStream struct {
	closeCount int
}

func (s *closeCountingModelStream) Recv() (model.Chunk, error) { return model.Chunk{}, io.EOF }
func (s *closeCountingModelStream) Close() error {
	s.closeCount++
	return nil
}
func (s *closeCountingModelStream) Metadata() map[string]any { return nil }

type contractModelClient struct {
	completeResp *model.Response
	completeErr  error
	streamer     model.Streamer
	streamErr    error
}

func (c contractModelClient) Complete(context.Context, *model.Request) (*model.Response, error) {
	return c.completeResp, c.completeErr
}

func (c contractModelClient) Stream(context.Context, *model.Request) (model.Streamer, error) {
	return c.streamer, c.streamErr
}

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

func TestEventInterceptorReplacementPersistsMutatedEventTypeEverywhere(t *testing.T) {
	rl := &recordingRunlog{}
	bus := hooks.NewBus()
	store := sessioninmem.New()
	sink := &recordingStreamSink{}
	replacement := hooks.NewAssistantMessageEvent("run-1", "svc.agent", "sess-1", "mutated assistant", nil)
	replacementInput, err := hooks.EncodeToHookInput(replacement, "turn-1")
	require.NoError(t, err)
	rt := New(
		WithRunEventStore(rl),
		WithHooks(bus),
		WithSessionStore(store),
		WithStream(sink),
		WithInterceptors(RuntimeInterceptorFuncs{
			BeforeEventFunc: func(_ context.Context, input *BeforeEventInput) (*BeforeEventDecision, error) {
				require.Equal(t, hooks.PlannerNote, input.Event.Type())
				return &BeforeEventDecision{
					Event:   replacement,
					Payload: replacementInput.Payload.RawMessage(),
				}, nil
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

	input, err := hooks.EncodeToHookInput(hooks.NewPlannerNoteEvent("run-1", "svc.agent", "sess-1", "original note", nil), "turn-1")
	require.NoError(t, err)
	require.NoError(t, rt.hookActivity(context.Background(), input))

	require.Len(t, rl.events, 1)
	require.Equal(t, hooks.AssistantMessage, rl.events[0].Type)
	require.NotNil(t, published)
	require.Equal(t, hooks.AssistantMessage, published.Type())
	streamEvents := sink.snapshot()
	require.Len(t, streamEvents, 1)
	require.Equal(t, stream.EventAssistantReply, streamEvents[0].Type())
}

func TestModelInterceptorStreamShortCircuitResponseDoesNotReturnNilStreamer(t *testing.T) {
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		BeforeModelFunc: func(context.Context, *BeforeModelInput) (*BeforeModelDecision, error) {
			return &BeforeModelDecision{
				Response: &model.Response{
					Content: []model.Message{{
						Role:  model.ConversationRoleAssistant,
						Parts: []model.Part{model.TextPart{Text: "short-circuited"}},
					}},
				},
			}, nil
		},
	}))
	var calls []string
	rt.models["default"] = interceptableModelClient{calls: &calls}
	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		turnID:  "turn-1",
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	streamer, err := client.Stream(context.Background(), &model.Request{Model: "test"})
	require.ErrorContains(t, err, "model response short-circuit is unsupported for streaming")
	require.Nil(t, streamer)
	require.Empty(t, calls)
}

func TestModelInterceptorStreamAfterErrorClosesOpenedStreamer(t *testing.T) {
	afterErr := errors.New("after blocked")
	opened := &closeCountingModelStream{}
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterModelFunc: func(context.Context, *AfterModelInput) (*AfterModelDecision, error) {
			return nil, afterErr
		},
	}))
	rt.models["default"] = closeCountingModelClient{stream: opened}
	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		turnID:  "turn-1",
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	streamer, err := client.Stream(context.Background(), &model.Request{Model: "test"})
	require.ErrorIs(t, err, afterErr)
	require.Nil(t, streamer)
	require.Equal(t, 1, opened.closeCount)
}

func TestModelInterceptorAfterModelEmptyDecisionCannotClearModelError(t *testing.T) {
	modelErr := errors.New("provider failed")
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterModelFunc: func(context.Context, *AfterModelInput) (*AfterModelDecision, error) {
			return &AfterModelDecision{}, nil
		},
	}))
	rt.models["default"] = contractModelClient{completeErr: modelErr}
	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		turnID:  "turn-1",
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	resp, err := client.Complete(context.Background(), &model.Request{Model: "test"})
	require.ErrorIs(t, err, modelErr)
	require.Nil(t, resp)
}

func TestModelInterceptorAfterModelReplacementResponseClearsModelError(t *testing.T) {
	modelErr := errors.New("provider failed")
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterModelFunc: func(context.Context, *AfterModelInput) (*AfterModelDecision, error) {
			return &AfterModelDecision{
				Response: &model.Response{
					Content: []model.Message{{
						Role:  model.ConversationRoleAssistant,
						Parts: []model.Part{model.TextPart{Text: "replacement"}},
					}},
				},
			}, nil
		},
	}))
	rt.models["default"] = contractModelClient{completeErr: modelErr}
	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		turnID:  "turn-1",
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	resp, err := client.Complete(context.Background(), &model.Request{Model: "test"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "replacement", resp.Content[0].Parts[0].(model.TextPart).Text)
}

func TestModelInterceptorAfterModelEnforcesNonNilResponseContract(t *testing.T) {
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterModelFunc: func(context.Context, *AfterModelInput) (*AfterModelDecision, error) {
			return nil, nil
		},
	}))
	rt.models["default"] = contractModelClient{}
	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		turnID:  "turn-1",
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	resp, err := client.Complete(context.Background(), &model.Request{Model: "test"})
	require.ErrorContains(t, err, "model complete contract violation")
	require.Nil(t, resp)
}

func TestModelInterceptorAfterModelStreamCannotClearModelError(t *testing.T) {
	modelErr := errors.New("stream failed")
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterModelFunc: func(context.Context, *AfterModelInput) (*AfterModelDecision, error) {
			return &AfterModelDecision{}, nil
		},
	}))
	rt.models["default"] = contractModelClient{streamErr: modelErr}
	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		turnID:  "turn-1",
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	streamer, err := client.Stream(context.Background(), &model.Request{Model: "test"})
	require.ErrorIs(t, err, modelErr)
	require.Nil(t, streamer)
}

func TestModelInterceptorAfterModelStreamEnforcesNonNilStreamerContract(t *testing.T) {
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{
		AfterModelFunc: func(context.Context, *AfterModelInput) (*AfterModelDecision, error) {
			return nil, nil
		},
	}))
	rt.models["default"] = contractModelClient{}
	ctx := newAgentContext(agentContextOptions{
		runtime: rt,
		agentID: "svc.agent",
		runID:   "run-1",
		turnID:  "turn-1",
	})
	client, ok := ctx.ModelClient("default")
	require.True(t, ok)

	streamer, err := client.Stream(context.Background(), &model.Request{Model: "test"})
	require.ErrorContains(t, err, "model stream contract violation")
	require.Nil(t, streamer)
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
