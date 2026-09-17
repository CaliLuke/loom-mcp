package planner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// TestWorkflowPlannersHonorForcedFinalization checks that policy caps stop pending work.
func TestWorkflowPlannersHonorForcedFinalization(t *testing.T) {
	for _, workflow := range []struct {
		name    string
		planner Planner
	}{
		{
			name: "sequential",
			planner: NewSequentialWorkflowPlanner(SequentialWorkflowConfig{
				Steps:        []WorkflowStep{{Name: "first", Tool: "test.first"}, {Name: "next", Tool: "test.next"}},
				FinalMessage: "workflow ended",
			}),
		},
		{
			name: "graph",
			planner: NewGraphWorkflowPlanner(WorkflowGraphConfig{
				Nodes: []WorkflowNode{
					{ID: "first", Tool: "test.first"},
					{ID: "next", Tool: "test.next", DependsOn: []string{"first"}},
				},
				FinalMessage: "workflow ended",
			}),
		},
	} {
		for _, reason := range []TerminationReason{TerminationReasonTimeBudget, TerminationReasonToolCap, TerminationReasonFailureCap} {
			for _, failed := range []bool{false, true} {
				history := "completed step"
				output := &ToolOutput{ToolCallID: "first"}
				if failed {
					history = "failed step"
					output.Error = NewToolError("step failed")
				}
				t.Run(workflow.name+"/"+string(reason)+"/"+history, func(t *testing.T) {
					result, err := workflow.planner.PlanResume(context.Background(), &PlanResumeInput{
						ToolOutputs: []*ToolOutput{output},
						Finalize:    &Termination{Reason: reason},
					})
					require.NoError(t, err)
					require.NotNil(t, result)
					assert.Empty(t, result.ToolCalls)
					assert.Nil(t, result.Await)
					require.NotNil(t, result.FinalResponse)
					assert.Equal(t, &model.Message{
						Role:  model.ConversationRoleAssistant,
						Parts: []model.Part{model.TextPart{Text: "workflow ended"}},
					}, result.FinalResponse.Message)
				})
			}
		}
	}
}
