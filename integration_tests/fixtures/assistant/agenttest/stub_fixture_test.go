package agenttest_test

import (
	"context"
	"testing"

	generated "example.com/assistant/gen/assistant/agents/assistant_runtime"
	projected "example.com/assistant/gen/assistant/toolsets/projected"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStubGeneratedFixtureWithoutExecutor(t *testing.T) {
	h := agenttest.New(t, func(ctx context.Context, rt *runtime.Runtime) error {
		if err := generated.RegisterUsedToolsets(ctx, rt); err != nil {
			return err
		}
		return generated.RegisterAssistantRuntimeAgent(ctx, rt, generated.AssistantRuntimeAgentConfig{Planner: fixturePlanner{}})
	}, agenttest.ScriptedModel("fixture",
		agenttest.Respond(agenttest.RawToolCall(projected.ProjectedStatusTool, rawjson.Message(`{}`))),
		agenttest.Respond(agenttest.Text("ready")).When(agenttest.LastToolResult(projected.ProjectedStatusToolTool, "stub ready"))))
	agenttest.Stub(h, projected.ProjectedStatusToolTool).Return(&projected.ProjectedStatusToolResult{Status: "stub ready"})
	result, err := h.Run(t.Context(), "assistant.assistant_runtime", nil)
	require.NoError(t, err)
	require.NoError(t, result.RunErr)
	expect.Require(t, expect.Trajectory(result.Trajectory), expect.Completed())
	found := false
	for _, step := range result.Trajectory.Steps {
		if step.Tool != nil {
			found = true
			assert.JSONEq(t, `{"status":"stub ready"}`, string(step.Tool.Result))
		}
	}
	assert.True(t, found)
}
