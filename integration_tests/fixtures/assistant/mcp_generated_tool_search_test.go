package assistantapi

import (
	"context"
	"encoding/json"
	"testing"

	assistant "example.com/assistant/gen/assistant"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newToolSearchAdapter(t *testing.T, opts *mcpassistant.ToolSearchOptions) *mcpassistant.MCPAdapter {
	t.Helper()

	adapter := mcpassistant.NewMCPAdapter(NewAssistant(), promptProvider{}, &mcpassistant.MCPAdapterOptions{
		ToolSearch: opts,
	})
	_, err := adapter.Initialize(context.Background(), &mcpassistant.InitializePayload{
		ProtocolVersion: "2025-06-18",
		ClientInfo: &mcpassistant.ClientInfo{
			Name:    "tool-search-test",
			Version: "1.0.0",
		},
	})
	require.NoError(t, err)
	return adapter
}

func toolNames(tools []*mcpassistant.ToolInfo) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool != nil {
			names = append(names, tool.Name)
		}
	}
	return names
}

func TestGeneratedAdapterToolSearchListsPinnedAndSyntheticTools(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{
		AlwaysVisible: []string{"search"},
	})

	result, err := adapter.ToolsList(context.Background(), &mcpassistant.ToolsListPayload{})
	require.NoError(t, err)

	assert.Equal(t, []string{"search", "search_tools", "call_tool"}, toolNames(result.Tools))
}

func TestGeneratedAdapterToolSearchFindsToolsBySchemaText(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{MaxResults: 1})
	stream := &capturedToolsCallStream{}

	err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"pattern":"sentiment|keywords"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result mcpassistant.ToolsListResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	assert.Equal(t, []string{"analyze_sentiment"}, toolNames(result.Tools))
}

func TestGeneratedAdapterToolSearchDoesNotBlockDirectToolCalls(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "analyze_sentiment",
		Arguments: json.RawMessage(`{"text":"great"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result assistant.AnalyzeSentimentResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	require.NotNil(t, result.Sentiment)
	assert.Equal(t, "positive", *result.Sentiment)
}

func TestGeneratedAdapterToolSearchCallToolProxyExecutesDiscoveredTool(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "call_tool",
		Arguments: json.RawMessage(`{"name":"analyze_sentiment","arguments":{"text":"great"}}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result assistant.AnalyzeSentimentResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	require.NotNil(t, result.Sentiment)
	assert.Equal(t, "positive", *result.Sentiment)
}

func TestGeneratedAdapterToolSearchCallToolRejectsSyntheticTools(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "call_tool",
		Arguments: json.RawMessage(`{"name":"call_tool","arguments":{"name":"search"}}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)
	require.NotNil(t, stream.events[0].IsError)
	assert.True(t, *stream.events[0].IsError)
}
