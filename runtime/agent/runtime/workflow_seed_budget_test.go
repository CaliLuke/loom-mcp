package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type seedBudgetWorkflowContext struct {
	*testWorkflowContext
	now            time.Time
	seedDelay      time.Duration
	hookDelay      time.Duration
	planDelay      time.Duration
	resumeDelay    time.Duration
	duringProvided func()
}

func TestRunBudgetStartsAfterSeedContent(t *testing.T) {
	for _, budget := range []time.Duration{0, time.Second} {
		t.Run(budget.String(), func(t *testing.T) {
			rt := New()
			base, _, input := terminalPolicyRunInputs(t, rt)
			base.plannerOutputs = []*api.PlanActivityOutput{{Result: oracleFinal()}}
			wf := &seedBudgetWorkflowContext{testWorkflowContext: base, now: time.Unix(0, 0), seedDelay: 2 * time.Second}
			input.Messages = []*model.Message{contentMessage("seed")}
			input.Policy = &PolicyOverrides{TimeBudget: budget, FinalizerGrace: time.Second}

			state, err := rt.startWorkflowRun(wf, AgentRegistration{ID: input.AgentID, PlanActivityName: "plan"}, input, workflowRunContext(input), "turn-1")
			require.NoError(t, err)
			assert.Equal(t, time.Unix(0, 0).Add(2*time.Second), wf.Now())
			if budget == 0 {
				assert.True(t, state.deadlines.Budget.IsZero())
				assert.True(t, state.deadlines.Hard.IsZero())
			} else {
				assert.Equal(t, wf.Now().Add(budget), state.deadlines.Budget)
				assert.Equal(t, state.deadlines.Budget.Add(state.timing.FinalizerGrace), state.deadlines.Hard)
				assert.Equal(t, budget, base.lastPlannerCall.Options.StartToCloseTimeout)
				assert.Equal(t, budget, base.lastPlannerCall.Options.ScheduleToStartTimeout)
			}
		})
	}
}

func TestAwaitBarrierSignalsAroundContentActivityKeepOneResume(t *testing.T) {
	for _, duringContent := range []bool{false, true} {
		name := "clarification before external result"
		if duringContent {
			name = "clarification during external content activity"
		}
		t.Run(name, func(t *testing.T) {
			rt := New()
			base, _, input := terminalPolicyRunInputs(t, rt)
			spec := newAnyJSONSpec("external.draft", "external")
			registerTestTool(t, rt, spec, nil)
			call := planner.AwaitToolItem{Name: spec.Name, ToolCallID: "draft-1", Payload: rawjson.Message(`{"topic":"loom"}`)}
			clarification := planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "topic", Question: "Which topic?"})
			external := planner.AwaitExternalToolsItem(&planner.AwaitExternalTools{ID: "draft", Items: []planner.AwaitToolItem{call}})
			items := []planner.AwaitItem{clarification, external}
			if duringContent {
				items = []planner.AwaitItem{external, clarification}
			}
			base.plannerOutputs = []*api.PlanActivityOutput{
				{Result: &planner.PlanResult{Await: planner.NewAwait(items...)}},
				{Result: oracleFinal()},
				{Result: oracleFinal()}, // Allows an erroneous budget finalization to complete before the count assertion.
			}
			wf := &seedBudgetWorkflowContext{testWorkflowContext: base, now: time.Unix(0, 0), seedDelay: 100 * time.Millisecond, hookDelay: 100 * time.Millisecond, planDelay: 200 * time.Millisecond, resumeDelay: 50 * time.Millisecond}
			input.Messages = []*model.Message{contentMessage("await")}
			input.Policy = &PolicyOverrides{TimeBudget: time.Second, FinalizerGrace: time.Second}
			answerRef, err := rt.RunStore.Put(t.Context(), input.RootRunID, []byte("use loom"))
			require.NoError(t, err)
			resultRef, err := rt.RunStore.Put(t.Context(), input.RootRunID, []byte(`{"ok":true}`))
			require.NoError(t, err)
			base.ensureSignals()
			answer := &api.ClarificationAnswer{ID: "topic", AnswerRef: answerRef}
			if duringContent {
				wf.duringProvided = func() {
					base.clarifyCh <- answer
				}
			} else {
				base.clarifyCh <- answer
			}
			base.toolResultsCh <- &api.ToolResultsSet{ID: "draft", Results: []*api.ProvidedToolResult{{Name: spec.Name, ToolCallID: call.ToolCallID, Refs: tools.ResultRefs{Result: resultRef}}}}
			out, _, err := rt.executeWorkflowRun(wf, AgentRegistration{ID: input.AgentID, PlanActivityName: "plan", ResumeActivityName: "resume", Specs: []tools.ToolSpec{spec}}, input, workflowRunContext(input), "turn-1", interrupt.NewController(wf))
			require.NoError(t, err)
			require.NotNil(t, out)
			require.Len(t, base.plannerCalls, 2, "one start and one resume, with no budget finalization")
			resume := base.plannerCalls[1].Input
			assert.Nil(t, resume.Finalize)
			view := testPlannerView(t, rt, resume)
			assert.Len(t, view.Outputs, 1)
			seed := contentMessage("await")
			assistant := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: call.ToolCallID, Name: string(call.Name), Input: call.Payload}}}
			result := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: call.ToolCallID, Content: map[string]any{"ok": true}}}}
			answerMessage := contentMessage("use loom")
			want := []*model.Message{seed, assistant, answerMessage, result}
			if duringContent {
				want = []*model.Message{seed, assistant, result, answerMessage}
			}
			assertIndexMessages(t, want, view.Messages)
		})
	}
}

func (w *seedBudgetWorkflowContext) Context() context.Context {
	return engine.WithWorkflowContext(w.testWorkflowContext.ctx, w)
}

func (w *seedBudgetWorkflowContext) Now() time.Time {
	return w.now
}

// ExecuteRecordContentActivity advances workflow time without a wall-clock sleep,
// and can deliver the other await signal while provided-result content is stored.
func (w *seedBudgetWorkflowContext) ExecuteRecordContentActivity(ctx context.Context, call engine.RecordContentActivityCall) (*api.RecordContentOutput, error) {
	if call.Input.SeedMessages {
		w.now = w.now.Add(w.seedDelay)
	} else if call.Input.Provided != nil {
		w.now = w.now.Add(100 * time.Millisecond)
		if w.duringProvided != nil {
			w.duringProvided()
		}
	}
	return w.testWorkflowContext.ExecuteRecordContentActivity(engine.WithActivityContext(ctx), call)
}

func (w *seedBudgetWorkflowContext) ExecutePlannerActivity(ctx context.Context, call engine.PlannerActivityCall) (*api.PlanActivityOutput, error) {
	delay := w.planDelay
	if call.Name == "resume" {
		delay = w.resumeDelay
	}
	w.now = w.now.Add(delay)
	return w.testWorkflowContext.ExecutePlannerActivity(engine.WithActivityContext(ctx), call)
}

func (w *seedBudgetWorkflowContext) PublishHook(ctx context.Context, call engine.HookActivityCall) error {
	w.now = w.now.Add(w.hookDelay)
	return w.testWorkflowContext.PublishHook(engine.WithActivityContext(ctx), call)
}
