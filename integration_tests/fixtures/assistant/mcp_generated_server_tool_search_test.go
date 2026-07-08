package assistantapi

import (
	"context"
	"testing"
	"time"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newToolSearchSDKSession(t *testing.T, opts *mcpassistant.ToolSearchOptions) *sdkmcp.ClientSession {
	t.Helper()

	_, server := newGeneratedSDKServerWithAdapterOptions(t, &mcpassistant.MCPAdapterOptions{
		ToolSearch: opts,
	})
	t.Cleanup(server.Close)
	session := connectSDKSessionToServer(t, server.URL+"/rpc", nil)
	t.Cleanup(func() {
		require.NoError(t, session.Close())
	})
	return session
}

func TestGeneratedSDKServerToolSearchUsesCompactCatalog(t *testing.T) {
	t.Parallel()

	session := newToolSearchSDKSession(t, &mcpassistant.ToolSearchOptions{AlwaysVisible: []string{"search"}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"search_tools", "call_tool", "search"}, sdkToolNames(result.Tools))
}

func TestGeneratedSDKServerToolSearchRejectsDirectHiddenCalls(t *testing.T) {
	t.Parallel()

	session := newToolSearchSDKSession(t, &mcpassistant.ToolSearchOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "analyze_sentiment",
		Arguments: map[string]any{
			"text": "great",
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "analyze_sentiment")
}

func TestGeneratedSDKServerToolSearchRejectsDirectHiddenCompatOption(t *testing.T) {
	t.Parallel()

	_, err := mcpassistant.NewSDKServer(NewAssistant(), &mcpassistant.SDKServerOptions{
		PromptProvider: promptProvider{},
		Adapter: &mcpassistant.MCPAdapterOptions{
			ToolSearch: &mcpassistant.ToolSearchOptions{AllowDirectHiddenCalls: true},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AllowDirectHiddenCalls")
}

func TestGeneratedSDKServerToolSearchCallToolInvokesHiddenTool(t *testing.T) {
	t.Parallel()

	session := newToolSearchSDKSession(t, &mcpassistant.ToolSearchOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "call_tool",
		Arguments: map[string]any{
			"name": "analyze_sentiment",
			"arguments": map[string]any{
				"text": "great",
			},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.NotNil(t, result.StructuredContent)
	structured, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "positive", structured["sentiment"])
}

func TestGeneratedSDKServerToolSearchIncludesProjectedTools(t *testing.T) {
	t.Parallel()

	session := newToolSearchSDKSession(t, &mcpassistant.ToolSearchOptions{AlwaysVisible: []string{"projected_lookup_tool"}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	listResult, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	assert.Contains(t, sdkToolNames(listResult.Tools), "projected_lookup_tool")

	callResult, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "call_tool",
		Arguments: map[string]any{
			"name": "projected_lookup_tool",
			"arguments": map[string]any{
				"query": "loom",
			},
		},
	})
	require.NoError(t, err)
	assert.False(t, callResult.IsError)
	structured, ok := callResult.StructuredContent.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "projected:loom", structured["answer"])
}

func TestGeneratedSDKServerToolSearchFindsProjectedTool(t *testing.T) {
	t.Parallel()

	session := newToolSearchSDKSession(t, &mcpassistant.ToolSearchOptions{MaxResults: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "search_tools",
		Arguments: map[string]any{
			"query":           "projected lookup",
			"include_schemas": true,
		},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	structured, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	tools, ok := structured["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "projected_lookup_tool", tool["name"])
	assert.Equal(t, "call_tool", tool["call_tool_name"])
	assert.Contains(t, tool["call_tool_json"], `"name": "projected_lookup_tool"`)
	assert.NotNil(t, tool["inputSchema"])
	assert.NotNil(t, tool["outputSchema"])
}

func TestGeneratedSDKServerToolSearchAcceptsOmittedSearchArguments(t *testing.T) {
	t.Parallel()

	session := newToolSearchSDKSession(t, &mcpassistant.ToolSearchOptions{MaxResults: 2})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "search_tools",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	structured, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	tools, ok := structured["tools"].([]any)
	require.True(t, ok)
	assert.Len(t, tools, 2)
	assert.Greater(t, structured["total_matches"], float64(2))
	assert.True(t, structured["truncated"].(bool))
}

func TestGeneratedSDKServerToolSearchReturnsCallToolExample(t *testing.T) {
	t.Parallel()

	session := newToolSearchSDKSession(t, &mcpassistant.ToolSearchOptions{MaxResults: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "search_tools",
		Arguments: map[string]any{
			"query": "document lookup",
		},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	structured, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	tools, ok := structured["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "search", tool["name"])
	assert.Equal(t, "call_tool", tool["call_tool_name"])
	assert.Contains(t, tool["call_tool_json"], `"name": "search"`)
}

func TestGeneratedSDKServerToolSearchIncludesOptionalCallTemplateArguments(t *testing.T) {
	t.Parallel()

	session := newToolSearchSDKSession(t, &mcpassistant.ToolSearchOptions{MaxResults: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "search_tools",
		Arguments: map[string]any{
			"query": "records",
		},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	structured, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	tools, ok := structured["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "search_records", tool["name"])
	assert.Contains(t, tool["call_tool_json"], `"query": "login"`)
}

func TestGeneratedSDKServerToolSearchExactNameSuppressesWeakMatches(t *testing.T) {
	t.Parallel()

	session := newToolSearchSDKSession(t, &mcpassistant.ToolSearchOptions{MaxResults: 10})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "search_tools",
		Arguments: map[string]any{
			"query": "summarize_text",
		},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	structured, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	tools, ok := structured["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "summarize_text", tool["name"])
	whyMatched, ok := tool["why_matched"].([]any)
	require.True(t, ok)
	assert.Contains(t, whyMatched, "exact tool name match")
}

func sdkToolNames(tools []*sdkmcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool != nil {
			names = append(names, tool.Name)
		}
	}
	return names
}
