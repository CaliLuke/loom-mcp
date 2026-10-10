package assistantapi

import (
	"context"
	"encoding/json/v2"
	"errors"
	"testing"

	"example.com/assistant/gen/assistant"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// payloadRecordingService lets the activity finish without a result value, so
// this test exercises payload capture independently of MCP result adaptation.
type payloadRecordingService struct{ assistant.Service }

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

func TestLocalMCPActivityRecordsExecutedPayload(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		marker  string
	}{
		{"projected_bounded_lookup_tool", `{"query":"loom"}`, "session-private"},
		{"search", `{"query":"loom"}`, "loom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := mcpassistant.NewMCPAdapter(&payloadRecordingService{Service: NewAssistant()}, promptProvider{}, &mcpassistant.MCPAdapterOptions{
				ToolSearch: &mcpassistant.ToolSearchOptions{AlwaysVisible: []string{tc.name}},
			})
			registration, err := mcpassistant.NewAssistantAssistantMcpLocalToolsetRegistration(adapter)
			require.NoError(t, err)
			rt := agentsruntime.New()
			require.NoError(t, rt.RegisterToolset(registration))
			out, err := rt.ExecuteToolActivity(t.Context(), &agentsruntime.ToolInput{
				RunID: "child", RootRunID: "root", SessionID: "session-private", ToolName: tools.Ident(tc.name),
				ToolCallID: "local", Payload: rawjson.Message(tc.payload),
			})
			require.NoError(t, err)
			require.Contains(t, out.Error, "deliberate service failure")
			assert.Empty(t, out.Refs.Result)
			payload, err := rt.RunStore.Get(t.Context(), "root", out.Refs.Payload)
			require.NoError(t, err)
			assert.Contains(t, string(payload), tc.marker)
			assert.NotContains(t, string(out.Payload), string(out.Refs.Payload))
		})
	}
}

func (*payloadRecordingService) Search(context.Context, *assistant.SearchPayload) (*assistant.SearchResult, error) {
	return nil, errors.New("deliberate service failure")
}

func (*payloadRecordingService) ProjectedBoundedLookup(context.Context, *assistant.ProjectedBoundedLookupPayload) (*assistant.ProjectedBoundedLookupResult, error) {
	return nil, errors.New("deliberate service failure")
}
