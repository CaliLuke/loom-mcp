package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAwaitTimeBudget(t *testing.T) {
	await := func() *planner.PlanResult {
		return &planner.PlanResult{Await: planner.NewAwait(planner.AwaitClarificationItem(&planner.AwaitClarification{
			ID: "topic", Question: "Which topic?",
		}))}
	}
	for _, tc := range []struct {
		name           string
		planDelay      time.Duration
		expireOn       hooks.EventType
		resumeResult   *planner.PlanResult
		wantResumes    int
		wantFinalizers int
		wantFinal      string
		wantToolResult bool
	}{
		{name: "expired before barrier", planDelay: time.Second, resumeResult: oracleFinal(), wantFinalizers: 1, wantFinal: "done"},
		{name: "expired inside barrier", expireOn: hooks.RunPaused, resumeResult: oracleFinal(), wantFinalizers: 1, wantFinal: "done"},
		{name: "terminal adoption crosses budget", expireOn: hooks.PlannerStep, resumeResult: oracleFinal(), wantResumes: 1, wantFinal: "done"},
		{name: "terminal tool result adoption crosses budget", expireOn: hooks.PlannerStep, resumeResult: &planner.PlanResult{FinalToolResult: &planner.FinalToolResult{Result: rawjson.Message(`{"ok":true}`)}}, wantResumes: 1, wantToolResult: true},
		{name: "nonterminal adoption crosses budget", expireOn: hooks.PlannerStep, resumeResult: await(), wantResumes: 1, wantFinalizers: 1, wantFinal: "budget final"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New()
			base, _, input := terminalPolicyRunInputs(t, rt)
			base.plannerOutputs = []*api.PlanActivityOutput{
				{Result: await()},
				{Result: tc.resumeResult},
				{Result: &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: contentMessage("budget final")}}},
			}
			wf := &seedBudgetWorkflowContext{testWorkflowContext: base, now: time.Unix(0, 0), planDelay: tc.planDelay}
			wf.duringHook = func(call engine.HookActivityCall) {
				if call.Input.Type == tc.expireOn && (tc.expireOn != hooks.PlannerStep || len(base.plannerCalls) == 2) {
					wf.now = time.Unix(0, 0).Add(2 * time.Second)
				}
			}
			input.Policy = &PolicyOverrides{TimeBudget: time.Second, FinalizerGrace: 10 * time.Second}
			answerRef, err := rt.RunStore.Put(t.Context(), input.RootRunID, []byte("loom"))
			require.NoError(t, err)
			base.ensureSignals()
			base.clarifyCh <- &api.ClarificationAnswer{ID: "topic", AnswerRef: answerRef, AnswerBytes: new(len("loom"))}

			out, _, err := rt.executeWorkflowRun(wf, AgentRegistration{ID: input.AgentID, PlanActivityName: "plan", ResumeActivityName: "resume"}, input, &workflowPlanInput{AgentID: input.AgentID, RunContext: workflowRunContext(input)}, "turn-1", interrupt.NewController(wf))

			require.NoError(t, err)
			require.NotNil(t, out)
			if tc.wantToolResult {
				assert.Nil(t, out.Final)
				if assert.NotNil(t, out.FinalToolResult) {
					assert.JSONEq(t, `{"ok":true}`, string(out.FinalToolResult.Result))
				}
			} else {
				assert.Equal(t, contentMessage(tc.wantFinal), out.Final)
			}
			var resumes, finalizers int
			for _, call := range base.plannerCalls {
				assert.Positive(t, call.Options.StartToCloseTimeout, "planner activity must have a positive timeout")
				if call.Input.Finalize != nil {
					finalizers++
					assert.Equal(t, planner.TerminationReasonTimeBudget, call.Input.Finalize.Reason)
				} else if call.Name == "resume" {
					resumes++
				}
			}
			assert.Equal(t, tc.wantResumes, resumes, "normal resumes")
			assert.Equal(t, tc.wantFinalizers, finalizers, "time-budget finalizers")
		})
	}
}

func TestPlanActivityExpiredDeadlineDoesNotSchedule(t *testing.T) {
	for _, remaining := range []time.Duration{0, -time.Second} {
		t.Run(remaining.String(), func(t *testing.T) {
			rt := New()
			base, planInput, input := terminalPolicyRunInputs(t, rt)
			base.plannerOutputs = []*api.PlanActivityOutput{{Result: oracleFinal()}}
			wf := &seedBudgetWorkflowContext{testWorkflowContext: base, now: time.Unix(0, 0)}
			out, err := rt.runPlanActivity(wf, planInput, "resume", engine.ActivityOptions{}, PlanActivityInput{
				AgentID: input.AgentID, RunID: input.RunID, RunContext: planInput.RunContext,
			}, wf.Now().Add(remaining))
			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Nil(t, out)
			assert.Empty(t, base.plannerCalls)
		})
	}
}
