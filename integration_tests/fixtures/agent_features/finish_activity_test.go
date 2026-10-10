package agentfeatures_test

import (
	"context"
	"strings"
	"testing"

	"example.com/agentfeatures/gen/features"
	"example.com/agentfeatures/gen/features/agents/coordinator"
	agentworkflow "example.com/agentfeatures/gen/features/agents/coordinator/workflow"
	"example.com/agentfeatures/gen/features/toolsets/workflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type completionContentPlanner struct{}

func TestGeneratedCompletionToolStoredResult(t *testing.T) {
	for _, size := range []int{1024, 1_000_001} {
		t.Run(strings.Repeat("large", size/1_000_000)+"result", func(t *testing.T) {
			fx := newFeatureRuntime(t)
			message := strings.Repeat("x", size)
			exec := agentworkflow.NewCoordinatorWorkflowExec(agentworkflow.WithMethodEcho(func(context.Context, any) (any, error) {
				return &features.MethodEchoResult{OK: true, Message: message}, nil
			}))
			registerCoordinatorToolsets(t, t.Context(), fx.rt, exec)
			require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{Planner: completionContentPlanner{}}))
			_, err := fx.rt.CreateSession(t.Context(), "completion-session")
			require.NoError(t, err)
			out, err := coordinator.NewClient(fx.rt).Run(t.Context(), "completion-session", nil, agentsruntime.WithRunID("completion-root"), agentsruntime.WithRunCompletionTool(workflow.MethodEcho))
			if size > 1_000_000 {
				assert.ErrorContains(t, err, "final tool result too large")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, out.FinalToolResult)
			typed, err := workflow.MethodEchoResultCodec.FromJSON(out.FinalToolResult.Result.RawMessage())
			require.NoError(t, err)
			assert.Equal(t, &workflow.MethodEchoResult{OK: true, Message: message}, typed)
			assert.Equal(t, 1, out.ToolEventCount)
			// The finish activity must use the stored generated result, not stale bytes.
			selected := *out.FinalToolResult
			selected.Result = rawjson.Message(`{"ok":false,"message":"stale"}`)
			finished, err := fx.rt.FinishRunActivity(t.Context(), &api.FinishRunInput{RootRunID: "completion-root", FinalToolResult: &selected})
			require.NoError(t, err)
			assert.Equal(t, out.FinalToolResult.Result, finished.FinalToolResult.Result)
		})
	}
}

func (completionContentPlanner) PlanStart(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: workflow.MethodEcho, ToolCallID: "complete", Payload: rawjson.Message(`{"topic":"completion"}`)}}}, nil
}

func (completionContentPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	panic("completion tool must finish without resuming the planner")
}
