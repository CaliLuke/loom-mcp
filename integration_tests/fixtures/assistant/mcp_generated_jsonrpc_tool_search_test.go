package assistantapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	assistant "example.com/assistant/gen/assistant"
	mcpAssistantjsonrpcc "example.com/assistant/gen/jsonrpc/mcp_assistant/client"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	goahttp "github.com/CaliLuke/loom/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newToolSearchJSONRPCClient(t *testing.T, serverURL string) *mcpAssistantjsonrpcc.Client {
	t.Helper()

	u, err := url.Parse(serverURL)
	require.NoError(t, err)

	doer := &testSessionDoer{
		base: &http.Client{Timeout: 10 * time.Second},
	}
	client := mcpAssistantjsonrpcc.NewClient(
		u.Scheme,
		u.Host,
		doer,
		goahttp.RequestEncoder,
		goahttp.ResponseDecoder,
		false,
	)
	_, err = client.Initialize()(context.Background(), &mcpassistant.InitializePayload{
		ProtocolVersion: "2025-06-18",
		ClientInfo: &mcpassistant.ClientInfo{
			Name:    "jsonrpc-tool-search-test",
			Version: "1.0.0",
		},
	})
	require.NoError(t, err)
	return client
}

func TestGeneratedJSONRPCToolSearchUsesCompactCatalog(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServerWithAdapterOptions(t, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{AlwaysVisible: []string{"search"}},
	})
	defer server.Close()
	client := newToolSearchJSONRPCClient(t, server.URL)

	raw, err := client.ToolsList()(context.Background(), &mcpassistant.ToolsListPayload{})
	require.NoError(t, err)
	result := raw.(*mcpassistant.ToolsListResult)
	assert.Equal(t, []string{"search_tools", "call_tool", "search"}, toolNames(result.Tools))
}

func TestGeneratedJSONRPCToolSearchAlwaysVisibleProjectedTool(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServerWithAdapterOptions(t, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{AlwaysVisible: []string{"projected_lookup_tool"}},
	})
	defer server.Close()
	client := newToolSearchJSONRPCClient(t, server.URL)

	raw, err := client.ToolsList()(context.Background(), &mcpassistant.ToolsListPayload{})
	require.NoError(t, err)
	result := raw.(*mcpassistant.ToolsListResult)
	assert.Equal(t, []string{"search_tools", "call_tool", "projected_lookup_tool"}, toolNames(result.Tools))
}

func TestGeneratedJSONRPCToolSearchRejectsDirectHiddenCalls(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServerWithAdapterOptions(t, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{},
	})
	defer server.Close()
	client := newToolSearchJSONRPCClient(t, server.URL)

	raw, err := client.ToolsCall()(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "analyze_sentiment",
		Arguments: json.RawMessage(`{"text":"great"}`),
	})
	require.NoError(t, err)
	stream := raw.(*mcpAssistantjsonrpcc.ToolsCallClientStream)
	_, err = stream.Recv(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Unknown tool")
}

func TestGeneratedJSONRPCToolSearchCallToolInvokesHiddenTool(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServerWithAdapterOptions(t, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{},
	})
	defer server.Close()
	client := newToolSearchJSONRPCClient(t, server.URL)

	raw, err := client.ToolsCall()(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "call_tool",
		Arguments: json.RawMessage(`{"name":"analyze_sentiment","arguments":{"text":"great"}}`),
	})
	require.NoError(t, err)
	stream := raw.(*mcpAssistantjsonrpcc.ToolsCallClientStream)
	result, err := stream.Recv(context.Background())
	require.NoError(t, err)

	var sentiment assistant.AnalyzeSentimentResult
	require.NoError(t, json.Unmarshal(result.StructuredContent, &sentiment))
	require.NotNil(t, sentiment.Sentiment)
	assert.Equal(t, "positive", *sentiment.Sentiment)
}

func TestGeneratedJSONRPCToolSearchCallToolInvokesProjectedTool(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServerWithAdapterOptions(t, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{},
	})
	defer server.Close()
	client := newToolSearchJSONRPCClient(t, server.URL)

	raw, err := client.ToolsCall()(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "call_tool",
		Arguments: json.RawMessage(`{"name":"projected_lookup_tool","arguments":{"query":"loom"}}`),
	})
	require.NoError(t, err)
	stream := raw.(*mcpAssistantjsonrpcc.ToolsCallClientStream)
	result, err := stream.Recv(context.Background())
	require.NoError(t, err)

	var projected assistant.ProjectedLookupToolResult
	require.NoError(t, json.Unmarshal(result.StructuredContent, &projected))
	assert.Equal(t, "projected:loom", projected.Answer)
	assert.Equal(t, "runtime-toolset", projected.Source)
}

func TestGeneratedJSONRPCToolSearchSearchesQueryText(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServerWithAdapterOptions(t, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{MaxResults: 1},
	})
	defer server.Close()
	client := newToolSearchJSONRPCClient(t, server.URL)

	raw, err := client.ToolsCall()(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"sentiment"}`),
	})
	require.NoError(t, err)
	stream := raw.(*mcpAssistantjsonrpcc.ToolsCallClientStream)
	result, err := stream.Recv(context.Background())
	require.NoError(t, err)

	var list toolSearchResult
	require.NoError(t, json.Unmarshal(result.StructuredContent, &list))
	assert.Equal(t, []string{"analyze_sentiment"}, toolSearchDescriptorNames(list.Tools))
	assert.Equal(t, "sentiment", list.Query)
}

func TestGeneratedJSONRPCToolSearchMatchesNaturalLanguageTokenQuery(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServerWithAdapterOptions(t, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{MaxResults: 1},
	})
	defer server.Close()
	client := newToolSearchJSONRPCClient(t, server.URL)

	raw, err := client.ToolsCall()(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"document lookup"}`),
	})
	require.NoError(t, err)
	stream := raw.(*mcpAssistantjsonrpcc.ToolsCallClientStream)
	result, err := stream.Recv(context.Background())
	require.NoError(t, err)

	var list toolSearchResult
	require.NoError(t, json.Unmarshal(result.StructuredContent, &list))
	assert.Equal(t, []string{"search"}, toolSearchDescriptorNames(list.Tools))
	require.Len(t, list.Tools, 1)
	assert.NotEmpty(t, list.Tools[0].WhyMatched)
}

func TestGeneratedJSONRPCToolSearchFindsProjectedTool(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServerWithAdapterOptions(t, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{MaxResults: 1},
	})
	defer server.Close()
	client := newToolSearchJSONRPCClient(t, server.URL)

	raw, err := client.ToolsCall()(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"projected lookup","include_schemas":true}`),
	})
	require.NoError(t, err)
	stream := raw.(*mcpAssistantjsonrpcc.ToolsCallClientStream)
	result, err := stream.Recv(context.Background())
	require.NoError(t, err)

	var list toolSearchResult
	require.NoError(t, json.Unmarshal(result.StructuredContent, &list))
	assert.Equal(t, []string{"projected_lookup_tool"}, toolSearchDescriptorNames(list.Tools))
	require.Len(t, list.Tools, 1)
	assert.NotEmpty(t, list.Tools[0].InputSchema)
	assert.NotEmpty(t, list.Tools[0].OutputSchema)
	assert.Equal(t, "call_tool", list.Tools[0].CallToolName)
	assert.Contains(t, list.Tools[0].CallToolJSON, `"name": "projected_lookup_tool"`)
}

func TestGeneratedJSONRPCToolSearchExactNameSuppressesWeakMatches(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServerWithAdapterOptions(t, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{MaxResults: 10},
	})
	defer server.Close()
	client := newToolSearchJSONRPCClient(t, server.URL)

	raw, err := client.ToolsCall()(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"summarize_text"}`),
	})
	require.NoError(t, err)
	stream := raw.(*mcpAssistantjsonrpcc.ToolsCallClientStream)
	result, err := stream.Recv(context.Background())
	require.NoError(t, err)

	var list toolSearchResult
	require.NoError(t, json.Unmarshal(result.StructuredContent, &list))
	assert.Equal(t, []string{"summarize_text"}, toolSearchDescriptorNames(list.Tools))
	assert.Equal(t, 1, list.TotalMatches)
	require.Len(t, list.Tools, 1)
	assert.Contains(t, list.Tools[0].WhyMatched, "exact tool name match")
}

func TestGeneratedJSONRPCToolSearchAcceptsOmittedArguments(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServerWithAdapterOptions(t, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{MaxResults: 2},
	})
	defer server.Close()
	client := newToolSearchJSONRPCClient(t, server.URL)

	raw, err := client.ToolsCall()(context.Background(), &mcpassistant.ToolsCallPayload{
		Name: "search_tools",
	})
	require.NoError(t, err)
	stream := raw.(*mcpAssistantjsonrpcc.ToolsCallClientStream)
	result, err := stream.Recv(context.Background())
	require.NoError(t, err)

	var list toolSearchResult
	require.NoError(t, json.Unmarshal(result.StructuredContent, &list))
	assert.Len(t, list.Tools, 2)
	assert.True(t, list.Truncated)
}

func TestGeneratedJSONRPCToolSearchStructuredContentIncludesPattern(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServerWithAdapterOptions(t, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{},
	})
	defer server.Close()
	client := newToolSearchJSONRPCClient(t, server.URL)

	raw, err := client.ToolsCall()(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"pattern":"keyword.*"}`),
	})
	require.NoError(t, err)
	stream := raw.(*mcpAssistantjsonrpcc.ToolsCallClientStream)
	result, err := stream.Recv(context.Background())
	require.NoError(t, err)

	var list toolSearchResult
	require.NoError(t, json.Unmarshal(result.StructuredContent, &list))
	assert.Equal(t, "keyword.*", list.Pattern)
	assert.Contains(t, toolSearchDescriptorNames(list.Tools), "extract_keywords")
}

func TestGeneratedJSONRPCToolSearchReturnsStructuredDescriptors(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServerWithAdapterOptions(t, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{},
	})
	defer server.Close()
	client := newToolSearchJSONRPCClient(t, server.URL)

	raw, err := client.ToolsCall()(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"sentiment","include_schemas":true}`),
	})
	require.NoError(t, err)
	stream := raw.(*mcpAssistantjsonrpcc.ToolsCallClientStream)
	result, err := stream.Recv(context.Background())
	require.NoError(t, err)

	var list toolSearchResult
	require.NoError(t, json.Unmarshal(result.StructuredContent, &list))
	require.Len(t, list.Tools, 1)
	assert.Equal(t, "analyze_sentiment", list.Tools[0].Name)
	assert.Equal(t, "analysis", list.Tools[0].Category)
	assert.NotEmpty(t, list.Tools[0].InputSchema)
	assert.NotEmpty(t, list.Tools[0].OutputSchema)
	assert.Equal(t, "call_tool", list.Tools[0].CallToolName)
	assert.Contains(t, list.Tools[0].CallToolJSON, `"name": "analyze_sentiment"`)
	assert.Contains(t, list.Tools[0].CallToolJSON, `"arguments": {`)
	require.Len(t, result.Content, 1)
	require.NotNil(t, result.Content[0].Text)
	assert.Contains(t, *result.Content[0].Text, "Call this tool through call_tool. Do not call analyze_sentiment directly.")
}
