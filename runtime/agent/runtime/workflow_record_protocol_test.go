package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowRecordsValidatedProtocolVersion(t *testing.T) {
	for _, rewrite := range []bool{false, true} {
		t.Run(fmt.Sprintf("rewritten_effective_input=%v", rewrite), func(t *testing.T) {
			rt := New()
			rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{}, PlanActivityName: "plan", ResumeActivityName: "resume"}
			if rewrite {
				rt.interceptors = []Interceptor{RuntimeInterceptorFuncs{BeforeRunFunc: func(_ context.Context, input *BeforeRunInput) (*BeforeRunDecision, error) {
					changed := input.Input
					changed.WorkflowProtocolVersion += 9
					return &BeforeRunDecision{Input: &changed}, nil
				}}}
			}
			wf := &testWorkflowContext{ctx: t.Context(), runtime: rt, hookRuntime: rt, finishRuntime: rt, plannerOutputs: []*api.PlanActivityOutput{
				{Recovery: &api.ModelRecovery{Kind: model.OutputValidationOutputBounds, Correction: "retry", DisableTools: true, Attempt: 1}},
				{Recovery: &api.ModelRecovery{Kind: model.OutputValidationOutputBounds, Correction: "retry again", DisableTools: true, Attempt: 2}},
				{Result: assistantCommitResult()},
			}}
			input := &RunInput{AgentID: "agent", RootRunID: "root", RunID: "run", WorkflowProtocolVersion: api.WorkflowProtocolVersion, Policy: &PolicyOverrides{MaxRecoveryTurns: 1}}
			_, err := rt.ExecuteWorkflow(wf, input)
			require.NoError(t, err)
			page, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
			require.NoError(t, err)
			steps, seals := 0, 0
			for _, event := range page.Events {
				if event.Type != hooks.PlannerStep && event.Type != hooks.RunSealed {
					continue
				}
				var payload struct {
					ProtocolVersion int
				}
				require.NoError(t, json.Unmarshal(event.Payload, &payload))
				assert.Equal(t, input.WorkflowProtocolVersion, payload.ProtocolVersion)
				if event.Type == hooks.PlannerStep {
					steps++
				} else {
					seals++
				}
			}
			assert.Equal(t, 3, steps, "recovery and finalization retain the validated version")
			assert.Equal(t, 1, seals)
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
			require.NoError(t, err)
			assert.Equal(t, input.WorkflowProtocolVersion, record.Runs[0].WorkflowProtocolVersion)
		})
	}
}
