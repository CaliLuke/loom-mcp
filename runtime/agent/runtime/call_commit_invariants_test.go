package runtime

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallCommitStepsEnd(t *testing.T) {
	rt := New()
	rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{}, PlanActivityName: "plan", ResumeActivityName: "resume"}
	wf := &testWorkflowContext{ctx: t.Context(), runtime: rt, hookRuntime: rt, finishRuntime: rt, plannerOutputs: []*api.PlanActivityOutput{
		{Recovery: &api.ModelRecovery{Kind: model.OutputValidationOutputBounds, Correction: "retry", DisableTools: true, Attempt: 1}},
		{Recovery: &api.ModelRecovery{Kind: model.OutputValidationOutputBounds, Correction: "retry again", DisableTools: true, Attempt: 2}},
		{Result: assistantCommitResult()},
	}}
	_, err := rt.ExecuteWorkflow(wf, &RunInput{AgentID: "agent", RootRunID: "root", RunID: "run", WorkflowProtocolVersion: api.WorkflowProtocolVersion, Policy: &PolicyOverrides{MaxRecoveryTurns: 1}})
	require.NoError(t, err)
	require.Len(t, wf.plannerCalls, 3, "recovery allowance ends in bounded finalization")
	for i, call := range wf.plannerCalls {
		assert.Equal(t, i+1, call.Input.Step)
		assert.Equal(t, i+1, call.Input.RunContext.Attempt, "retry_same_attempt is prevented by bounded workflow recovery")
	}
	assert.NotNil(t, wf.plannerCalls[2].Input.Finalize)
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
	require.NoError(t, err)
	assert.True(t, record.Runs[0].Sealed)
	assert.NotEmpty(t, record.Runs[0].Transcript)
}
