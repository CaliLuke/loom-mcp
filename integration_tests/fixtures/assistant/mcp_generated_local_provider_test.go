package assistantapi

import (
	"context"
	"encoding/json/v2"
	"testing"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedLocalProviderBindsRuntimeToolMetadata(t *testing.T) {
	adapter := mcpassistant.NewMCPAdapter(NewAssistant(), promptProvider{}, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{AlwaysVisible: []string{"projected_bounded_lookup_tool"}},
	})
	registration, err := mcpassistant.NewAssistantAssistantMcpLocalToolsetRegistration(adapter)
	require.NoError(t, err)
	var projectedSpec *tools.ToolSpec
	for i := range registration.Specs {
		if registration.Specs[i].Name == "projected_bounded_lookup_tool" {
			projectedSpec = &registration.Specs[i]
			break
		}
	}
	require.NotNil(t, projectedSpec)
	assert.Equal(t, []string{"projected", "bounded"}, projectedSpec.Tags)
	assert.Equal(t, map[string][]string{"projection:test": {"preserved"}}, projectedSpec.Meta)
	execution, err := registration.Execute(context.Background(), &planner.ToolRequest{
		Name:             "projected_bounded_lookup_tool",
		Payload:          rawjson.Message(`{"query":"loom"}`),
		RunID:            "run-local",
		SessionID:        "session-local",
		TurnID:           "turn-local",
		ToolCallID:       "call-local",
		ParentToolCallID: "call-parent",
	})
	require.NoError(t, err)
	require.NotNil(t, execution)
	require.NotNil(t, execution.ToolResult)
	require.Nil(t, execution.ToolResult.Error)
	encoded, err := json.Marshal(execution.ToolResult.Result)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), "document:loom@session-local")
}
