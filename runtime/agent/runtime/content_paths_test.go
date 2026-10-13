package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"sync"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	engineinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contentPathWrite struct {
	scope string
	data  []byte
	hash  runstore.Hash
}
type contentPathStore struct {
	runstore.Store
	mu       sync.Mutex
	writes   []contentPathWrite
	attempts int
	failAt   int
}

func (s *contentPathStore) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.attempts == s.failAt {
		return "", runstore.ErrContentTooLarge
	}
	hash, err := s.Store.Put(ctx, scope, data)
	if err == nil {
		s.writes = append(s.writes, contentPathWrite{scope, append([]byte(nil), data...), hash})
	}
	return hash, err
}

func (s *contentPathStore) snapshot() []contentPathWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]contentPathWrite(nil), s.writes...)
}

func contentMessage(text string) *model.Message {
	return &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: text}}}
}

func registerContentAgent(t *testing.T, rt *Runtime, p planner.Planner) {
	t.Helper()
	require.NoError(t, rt.RegisterAgent(t.Context(), AgentRegistration{
		ID: "content.agent", Planner: p,
		Workflow:         engine.WorkflowDefinition{Name: "content.workflow", TaskQueue: "content.queue", Handler: rt.ExecuteWorkflow},
		PlanActivityName: "content.plan", ResumeActivityName: "content.resume", ExecuteToolActivity: "content.tool",
	}))
	_, err := rt.CreateSession(t.Context(), "session")
	require.NoError(t, err)
}

func TestContentRunStartRecordsEffectiveMessagesBeforePlanner(t *testing.T) {
	for _, failAt := range []int{0, 3, 4, 5} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			store := &contentPathStore{Store: storeinmem.New(), failAt: failAt}
			effective := []*model.Message{contentMessage("first"), contentMessage("rewritten"), contentMessage("third")}
			called := false
			// Isolate run-start content writes from the default continuation seed.
			rt := New(WithoutContextDefault("content.agent"), WithEngine(engineinmem.New()), WithRunStore(store), WithInterceptors(RuntimeInterceptorFuncs{
				BeforeRunFunc: func(_ context.Context, in *BeforeRunInput) (*BeforeRunDecision, error) {
					input := in.Input
					input.Messages = effective
					return &BeforeRunDecision{Input: &input}, nil
				},
			}))
			registerContentAgent(t, rt, &stubPlanner{start: func(_ context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
				called = true
				writes := store.snapshot()
				if assert.Len(t, writes, 6) {
					for i, w := range writes[2:5] {
						data, err := json.Marshal(effective[i])
						require.NoError(t, err)
						assert.Equal(t, "root", w.scope)
						assert.JSONEq(t, string(data), string(w.data))
					}
				}
				return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: contentMessage("done")}}, nil
			}})
			_, err := rt.MustClient("content.agent").Run(t.Context(), "session", []*model.Message{contentMessage("first"), contentMessage("original"), contentMessage("third")}, WithRunID("root"))
			if failAt > 0 {
				require.ErrorIs(t, err, runstore.ErrContentTooLarge)
				assert.False(t, called)
			} else {
				require.NoError(t, err)
				assert.True(t, called)
			}
		})
	}
}

func TestContentPlannerCallsStoredBeforeReturn(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, failAt := range []int{0, 1, 2, 3} {
			t.Run(fmt.Sprintf("resume=%v/fail=%d", resume, failAt), func(t *testing.T) {
				store := &contentPathStore{Store: storeinmem.New(), failAt: failAt}
				result := &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "runtime.tool_unavailable", ToolCallID: "one"}, {Name: "runtime.tool_unavailable", ToolCallID: "two"}}}
				p := &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
					in.Events.AssistantChunk(ctx, "turn")
					return result, nil
				}, resume: func(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
					in.Events.AssistantChunk(ctx, "turn")
					return result, nil
				}}
				rt := New(WithEngine(engineinmem.New()), WithRunStore(store))
				registerContentAgent(t, rt, p)
				input := &api.PlanActivityInput{AgentID: "content.agent", RunID: "child", RunContext: run.Context{RootRunID: "root"}}
				out, err := executeContentPlannerActivity(t, rt, input, resume)
				if failAt > 0 {
					require.ErrorIs(t, err, runstore.ErrContentTooLarge)
					assert.Nil(t, out)
					return
				}
				require.NoError(t, err)
				writes := store.snapshot()
				require.Len(t, writes, 3)
				require.NotEmpty(t, out.TranscriptRef)
				transcriptJSON, err := json.Marshal(testStoredTranscript(t, rt, out, "root"))
				require.NoError(t, err)
				assert.JSONEq(t, string(transcriptJSON), string(writes[0].data))
				assert.Equal(t, writes[0].hash, out.TranscriptRef)
				require.Len(t, out.ToolCallRefs, 2)
				writes = writes[1:]
				for i, w := range writes {
					data, err := json.Marshal(out.Result.ToolCalls[i])
					require.NoError(t, err)
					assert.Equal(t, w.hash, out.ToolCallRefs[i])
					assert.Equal(t, "root", w.scope)
					assert.JSONEq(t, string(data), string(w.data))
				}
			})
		}
	}
}

// executeContentPlannerActivity exercises the real in-memory activity boundary.
func executeContentPlannerActivity(t *testing.T, rt *Runtime, input *api.PlanActivityInput, resume bool) (*api.PlanActivityOutput, error) {
	t.Helper()
	var output *api.PlanActivityOutput
	require.NoError(t, rt.Engine.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "capture", Handler: func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		name := "content.plan"
		if resume {
			name = "content.resume"
		}
		var err error
		output, err = w.ExecutePlannerActivity(w.Context(), engine.PlannerActivityCall{Name: name, Input: input})
		return &api.RunOutput{}, err
	}}))
	handle, err := rt.Engine.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "capture", Workflow: "capture", Input: &api.RunInput{RootRunID: "root"}})
	require.NoError(t, err)
	_, err = handle.Wait(t.Context())
	return output, err
}
