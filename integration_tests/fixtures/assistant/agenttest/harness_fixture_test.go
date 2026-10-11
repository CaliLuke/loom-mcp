package agenttest_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	generated "example.com/assistant/gen/assistant/agents/assistant_runtime"
	projected "example.com/assistant/gen/assistant/toolsets/projected"
	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixturePlanner struct{}

func TestHarnessGeneratedFixtureGolden(t *testing.T) {
	register := func(ctx context.Context, rt *runtime.Runtime) error {
		executor := runtime.ToolCallExecutorFunc(func(_ context.Context, _ *runtime.ToolCallMeta, call *planner.ToolRequest) (*runtime.ToolExecutionResult, error) {
			return runtime.Executed(&planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: &projected.ProjectedStatusToolResult{Status: "ready"}}), nil
		})
		if err := generated.RegisterUsedToolsets(ctx, rt, generated.WithProjectedExecutor(executor)); err != nil {
			return err
		}
		return generated.RegisterAssistantRuntimeAgent(ctx, rt, generated.AssistantRuntimeAgentConfig{Planner: fixturePlanner{}})
	}
	h := agenttest.New(t, register, agenttest.ScriptedModel("fixture", agenttest.Respond(agenttest.RawToolCall(projected.ProjectedStatusTool, rawjson.Message(`{}`))), agenttest.Respond(agenttest.Text("ready"))))
	h.Passthrough(projected.ProjectedStatusToolTool)
	result, err := h.Run(t.Context(), "assistant.assistant_runtime", agenttest.User("status?"))
	require.NoError(t, err)
	require.NoError(t, result.RunErr)
	var shape strings.Builder
	fmt.Fprintf(&shape, "Run: %s\nSession: %s\nAgent: %s\nSealed: %t\n", result.RunID, result.SessionID, result.Trajectory.AgentID, result.Record.Runs[0].Sealed)
	for _, step := range result.Trajectory.Steps {
		if step.Model != nil {
			fmt.Fprintf(&shape, "Model: %s\n", step.Model.ModelID)
		}
		if step.Tool != nil {
			fmt.Fprintf(&shape, "Tool: %s Result: %s\n", step.Tool.Name, step.Tool.Result)
		}
	}
	shape.WriteString("Final: " + result.Output.Final.Parts[0].(model.TextPart).Text + "\n")
	golden, err := os.ReadFile("testdata/harness_fixture.golden")
	require.NoError(t, err)
	assert.Equal(t, string(golden), shape.String())
}

func (fixturePlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	return completeFixtureTurn(ctx, input.Agent, input.Messages)
}

func (fixturePlanner) PlanResume(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return completeFixtureTurn(ctx, input.Agent, input.Messages)
}

func completeFixtureTurn(ctx context.Context, pc planner.PlannerContext, messages []*model.Message) (*planner.PlanResult, error) {
	client, ok := pc.ModelClient("fixture")
	if !ok {
		return nil, fmt.Errorf("fixture model missing")
	}
	response, err := client.Complete(ctx, &model.Request{
		Model: "scripted", Messages: messages,
		Tools: []*model.ToolDefinition{{Name: projected.ProjectedStatusTool.String(), InputSchema: map[string]any{"type": "object"}}},
	})
	if err != nil {
		return nil, err
	}
	result := &planner.PlanResult{}
	for _, call := range response.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, planner.ToolRequest{Name: call.Name, ToolCallID: call.ID, Payload: call.Payload})
	}
	if len(result.ToolCalls) == 0 {
		result.FinalResponse = &planner.FinalResponse{Message: &response.Content[0]}
	}
	return result, nil
}
