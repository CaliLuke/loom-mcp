package runtime

import (
	"context"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlannerActivityRejectsBlankClarificationQuestion(t *testing.T) {
	for _, question := range []string{"", "  "} {
		for _, resume := range []bool{false, true} {
			for _, staged := range []bool{false, true} {
				t.Run(fmt.Sprintf("question=%q/resume=%v/staged=%v", question, resume, staged), func(t *testing.T) {
					store := &contentPathStore{Store: storeinmem.New()}
					bus := &recordingHooks{}
					sink := &recordingStreamSink{}
					rt := New(WithRunStore(store), WithHooks(bus), WithStream(sink))
					plan := func(ctx context.Context, events planner.PlannerEvents) (*planner.PlanResult, error) {
						if staged {
							presentations := events.(*runtimePlannerEvents)
							id := presentations.StartModelPresentation(ctx)
							if err := presentations.CommitModelPresentation(ctx, id, &model.Response{Content: []model.Message{{
								Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "pending response"}},
							}}}); err != nil {
								return nil, err
							}
							presentations.FinishModelPresentation(ctx, id, true)
						}
						return &planner.PlanResult{
							ToolCalls: []planner.ToolRequest{{Name: tools.ToolUnavailable, ToolCallID: "call"}},
							Await: planner.NewAwait(
								planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "valid", Question: "Which topic?"}),
								planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "blank", Question: question}),
							),
						}, nil
					}
					registerContentAgent(t, rt, &stubPlanner{
						start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
							return plan(ctx, in.Events)
						},
						resume: func(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
							return plan(ctx, in.Events)
						},
					})
					input := &PlanActivityInput{AgentID: "content.agent", RunID: "root", RunContext: run.Context{RootRunID: "root", RunID: "root", SessionID: "session"}}
					activity := rt.PlanStartActivity
					if resume {
						activity = rt.PlanResumeActivity
					}
					out, err := activity(t.Context(), input)

					assert.Nil(t, out)
					assert.Empty(t, bus.events, "invalid planner output must not publish durable events")
					assert.Empty(t, store.snapshot(), "invalid planner output must not store content")
					page, listErr := rt.RunEventStore.List(t.Context(), "root", "", 100)
					require.NoError(t, listErr)
					assert.Empty(t, page.Events)
					if staged {
						events := sink.snapshot()
						if assert.Len(t, events, 2) {
							assert.Equal(t, stream.ModelPresentationStarted, events[0].(stream.ModelPresentation).Data.State)
							assert.Equal(t, stream.ModelPresentationDiscarded, events[1].(stream.ModelPresentation).Data.State)
						}
					} else {
						assert.Empty(t, sink.snapshot())
					}
					var contract *engine.ApplicationError
					require.ErrorAs(t, err, &contract)
					assert.Equal(t, "planner_contract", contract.Type)
					assert.True(t, contract.NonRetryable)
					assert.Contains(t, contract.Message, "question")
				})
			}
		}
	}
}

func TestExecuteToolActivityRejectsBlankMissingFieldsQuestion(t *testing.T) {
	for _, question := range []string{"", "  "} {
		t.Run(fmt.Sprintf("question=%q", question), func(t *testing.T) {
			store := &contentPathStore{Store: storeinmem.New()}
			rt := New(WithRunStore(store))
			spec := newAnyJSONSpec("test.lookup", "test")
			require.NoError(t, rt.RegisterToolset(ToolsetRegistration{
				Name: "test", Specs: []tools.ToolSpec{spec},
				Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
					return Executed(&planner.ToolResult{
						Name: call.Name, ToolCallID: call.ToolCallID, Error: planner.NewToolError("missing topic"),
						RetryHint: &planner.RetryHint{Reason: planner.RetryReasonMissingFields, MissingFields: []string{"topic"}, ClarifyingQuestion: question},
					}), nil
				},
			}))

			out, err := rt.ExecuteToolActivity(t.Context(), &ToolInput{
				RootRunID: "root", RunID: "root", ToolName: spec.Name, ToolCallID: "call", Payload: []byte(`{}`),
			})

			assert.Nil(t, out)
			assert.Empty(t, store.snapshot())
			var contract *engine.ApplicationError
			require.ErrorAs(t, err, &contract)
			assert.Equal(t, "tool_result_contract", contract.Type)
			assert.True(t, contract.NonRetryable)
			assert.Contains(t, contract.Message, "question")
		})
	}
}
