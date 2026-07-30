package assistantapi

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	assistant "example.com/assistant/gen/assistant"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	loom "github.com/CaliLuke/loom/pkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type toolSearchResult struct {
	Tools        []toolSearchDescriptor `json:"tools"`
	TotalMatches int                    `json:"total_matches"`
	Truncated    bool                   `json:"truncated"`
	Query        string                 `json:"query,omitempty"`
	Pattern      string                 `json:"pattern,omitempty"`
}

type toolSearchDescriptor struct {
	Name              string          `json:"name"`
	Title             string          `json:"title,omitempty"`
	Description       string          `json:"description,omitempty"`
	InputSchema       json.RawMessage `json:"inputSchema,omitempty"`
	OutputSchema      json.RawMessage `json:"outputSchema,omitempty"`
	Annotations       json.RawMessage `json:"annotations,omitempty"`
	Meta              json.RawMessage `json:"_meta,omitempty"`
	Icons             []any           `json:"icons,omitempty"`
	Category          string          `json:"category,omitempty"`
	Tags              []string        `json:"tags,omitempty"`
	Keywords          []string        `json:"keywords,omitempty"`
	WhyMatched        []string        `json:"why_matched,omitempty"`
	CallToolName      string          `json:"call_tool_name,omitempty"`
	CallToolArguments json.RawMessage `json:"call_tool_arguments,omitempty"`
	CallToolJSON      string          `json:"call_tool_json,omitempty"`
}

func newToolSearchAdapter(t testing.TB, opts *mcpassistant.ToolSearchOptions) *mcpassistant.MCPAdapter {
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

func toolSearchDescriptorNames(tools []toolSearchDescriptor) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func boolValue(v bool) *bool {
	return &v
}

func callToolSearch(t testing.TB, adapter *mcpassistant.MCPAdapter, arguments string) toolSearchResult {
	t.Helper()

	stream := &capturedToolsCallStream{}
	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(arguments),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)
	require.NotNil(t, stream.events[0].StructuredContent)

	var result toolSearchResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	return result
}

func TestGeneratedAdapterToolSearchListsFullCatalogWhenDisabled(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, nil)

	result, err := adapter.ToolsList(context.Background(), &mcpassistant.ToolsListPayload{})
	require.NoError(t, err)

	names := toolNames(result.Tools)
	assert.Contains(t, names, "analyze_sentiment")
	assert.Contains(t, names, "extract_keywords")
	assert.Contains(t, names, "search")
	assert.NotContains(t, names, "search_tools")
	assert.NotContains(t, names, "call_tool")
}

func TestGeneratedAdapterToolSearchCompactsPublicCatalog(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{
		AlwaysVisible: []string{"search"},
	})

	result, err := adapter.ToolsList(context.Background(), &mcpassistant.ToolsListPayload{})
	require.NoError(t, err)

	assert.Equal(t, []string{"search_tools", "call_tool", "search"}, toolNames(result.Tools))
}

func TestGeneratedAdapterToolSearchAlwaysVisibleProjectedTool(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{
		AlwaysVisible: []string{"projected_lookup_tool"},
	})

	result, err := adapter.ToolsList(context.Background(), &mcpassistant.ToolsListPayload{})
	require.NoError(t, err)
	assert.Equal(t, []string{"search_tools", "call_tool", "projected_lookup_tool"}, toolNames(result.Tools))
}

func TestGeneratedAdapterToolSearchRejectsUnknownAlwaysVisibleTool(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		_ = newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{
			AlwaysVisible: []string{"does_not_exist"},
		})
	})
}

func TestGeneratedAdapterToolSearchPanicsOnSyntheticNameCollision(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		_ = newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{
			SearchToolName: "search",
		})
	})
	assert.Panics(t, func() {
		_ = newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{
			SearchToolName: "discover",
			CallToolName:   "discover",
		})
	})
}

func TestGeneratedAdapterToolSearchOverrideNames(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{
		SearchToolName: "discover_tools",
		CallToolName:   "invoke_tool",
	})

	result, err := adapter.ToolsList(context.Background(), &mcpassistant.ToolsListPayload{})
	require.NoError(t, err)
	assert.Equal(t, []string{"discover_tools", "invoke_tool"}, toolNames(result.Tools))
}

func TestGeneratedAdapterToolSearchSearchesQueryText(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{MaxResults: 1})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"sentiment"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result toolSearchResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	assert.Equal(t, []string{"analyze_sentiment"}, toolSearchDescriptorNames(result.Tools))
	assert.Equal(t, 1, result.TotalMatches)
	assert.False(t, result.Truncated)
	assert.Equal(t, "sentiment", result.Query)
}

func TestGeneratedAdapterToolSearchMatchesNaturalLanguageTokenQuery(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{MaxResults: 1})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"document lookup"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result toolSearchResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	assert.Equal(t, []string{"search"}, toolSearchDescriptorNames(result.Tools))
	require.Len(t, result.Tools, 1)
	assert.NotEmpty(t, result.Tools[0].WhyMatched)
}

func TestGeneratedAdapterToolSearchFindsProjectedTool(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{MaxResults: 1})
	result := callToolSearch(t, adapter, `{"query":"projected lookup","include_schemas":true}`)

	assert.Equal(t, []string{"projected_lookup_tool"}, toolSearchDescriptorNames(result.Tools))
	require.Len(t, result.Tools, 1)
	assert.NotEmpty(t, result.Tools[0].InputSchema)
	assert.NotEmpty(t, result.Tools[0].OutputSchema)
	assert.Equal(t, "call_tool", result.Tools[0].CallToolName)
	assert.Contains(t, result.Tools[0].CallToolJSON, `"name": "projected_lookup_tool"`)
	assert.Contains(t, result.Tools[0].CallToolJSON, `"query": "<string>"`)
}

func TestGeneratedAdapterToolSearchExactNameSuppressesWeakMatches(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{MaxResults: 10})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"summarize_text"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result toolSearchResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	assert.Equal(t, []string{"summarize_text"}, toolSearchDescriptorNames(result.Tools))
	assert.Equal(t, 1, result.TotalMatches)
	require.Len(t, result.Tools, 1)
	assert.Contains(t, result.Tools[0].WhyMatched, "exact tool name match")
}

func TestGeneratedAdapterToolSearchExactTitleSuppressesWeakMatches(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{MaxResults: 10})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"Search Knowledge Base"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result toolSearchResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	assert.Equal(t, []string{"search"}, toolSearchDescriptorNames(result.Tools))
	assert.Equal(t, 1, result.TotalMatches)
	require.Len(t, result.Tools, 1)
	assert.Contains(t, result.Tools[0].WhyMatched, "exact title match")
}

func TestGeneratedAdapterToolSearchBroadNaturalLanguageStillReturnsRelevantTools(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{MaxResults: 10})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"text analysis keywords sentiment"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result toolSearchResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	names := toolSearchDescriptorNames(result.Tools)
	assert.Contains(t, names, "analyze_sentiment")
	assert.Contains(t, names, "extract_keywords")
	assert.GreaterOrEqual(t, result.TotalMatches, 2)
	require.NotEmpty(t, result.Tools)
	assert.NotContains(t, result.Tools[0].WhyMatched, "exact tool name match")
}

func TestGeneratedAdapterToolSearchRankingQualityMatrix(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{MaxResults: 10})
	cases := []struct {
		name        string
		query       string
		wantTop     string
		wantNames   []string
		maxResults  int
		wantWhyPart string
	}{
		{
			name:        "exact snake case name",
			query:       "summarize_text",
			wantTop:     "summarize_text",
			wantNames:   []string{"summarize_text"},
			maxResults:  1,
			wantWhyPart: "exact tool name match",
		},
		{
			name:        "normalized name words",
			query:       "summarize text",
			wantTop:     "summarize_text",
			wantNames:   []string{"summarize_text"},
			maxResults:  1,
			wantWhyPart: "exact tool name match",
		},
		{
			name:        "exact human title",
			query:       "Search Knowledge Base",
			wantTop:     "search",
			wantNames:   []string{"search"},
			maxResults:  1,
			wantWhyPart: "exact title match",
		},
		{
			name:        "natural language metadata intent",
			query:       "document lookup",
			wantTop:     "search",
			wantNames:   []string{"search"},
			maxResults:  1,
			wantWhyPart: "matched discovery metadata token",
		},
		{
			name:        "typo fuzzy name intent",
			query:       "sumarize txt",
			wantTop:     "summarize_text",
			wantNames:   []string{"summarize_text"},
			maxResults:  1,
			wantWhyPart: "fuzzy tool name/title match",
		},
		{
			name:       "broad text analysis intent",
			query:      "text analysis keywords sentiment",
			wantTop:    "analyze_sentiment",
			wantNames:  []string{"analyze_sentiment", "extract_keywords"},
			maxResults: 4,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := callToolSearch(t, adapter, `{"query":`+strconv.Quote(tc.query)+`}`)
			names := toolSearchDescriptorNames(result.Tools)
			require.NotEmpty(t, names)
			assert.Equal(t, tc.wantTop, names[0])
			for _, want := range tc.wantNames {
				assert.Contains(t, names, want)
			}
			if tc.maxResults > 0 {
				assert.LessOrEqual(t, len(names), tc.maxResults)
			}
			if tc.wantWhyPart != "" {
				require.NotEmpty(t, result.Tools[0].WhyMatched)
				assert.Contains(t, result.Tools[0].WhyMatched, tc.wantWhyPart)
			}
			for _, tool := range result.Tools {
				assert.NotEmpty(t, tool.CallToolJSON)
				assert.Equal(t, "call_tool", tool.CallToolName)
			}
		})
	}
}

func TestGeneratedAdapterToolSearchTuningControlsRankingBreadth(t *testing.T) {
	t.Parallel()

	t.Run("fuzzy can be disabled", func(t *testing.T) {
		t.Parallel()

		adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{
			MaxResults:        10,
			FuzzyNameMatching: boolValue(false),
			BroadFallback:     boolValue(false),
			ExactMatchMode:    "narrow",
		})

		result := callToolSearch(t, adapter, `{"query":"sumarize txt"}`)
		assert.Empty(t, result.Tools)
		assert.Equal(t, 0, result.TotalMatches)
	})

	t.Run("fuzzy can recover typo intent", func(t *testing.T) {
		t.Parallel()

		adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{
			MaxResults:        10,
			FuzzyNameMatching: boolValue(true),
			BroadFallback:     boolValue(false),
		})

		result := callToolSearch(t, adapter, `{"query":"sumarize txt"}`)
		assert.Equal(t, []string{"summarize_text"}, toolSearchDescriptorNames(result.Tools))
		require.Len(t, result.Tools, 1)
		assert.Contains(t, result.Tools[0].WhyMatched, "fuzzy tool name/title match")
	})

	t.Run("broad fallback can be disabled", func(t *testing.T) {
		t.Parallel()

		adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{
			MaxResults:        10,
			FuzzyNameMatching: boolValue(false),
			BroadFallback:     boolValue(false),
		})

		result := callToolSearch(t, adapter, `{"query":"document lookup"}`)
		assert.Empty(t, result.Tools)
		assert.Equal(t, 0, result.TotalMatches)
	})

	t.Run("min score suppresses weak broad matches", func(t *testing.T) {
		t.Parallel()

		adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{
			MaxResults:        10,
			MinScore:          10_000,
			FuzzyNameMatching: boolValue(false),
		})

		result := callToolSearch(t, adapter, `{"query":"document lookup"}`)
		assert.Empty(t, result.Tools)
		assert.Equal(t, 0, result.TotalMatches)
	})

	t.Run("exact off broadens without losing top exact result", func(t *testing.T) {
		t.Parallel()

		adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{
			MaxResults:     10,
			ExactMatchMode: "off",
		})

		result := callToolSearch(t, adapter, `{"query":"summarize_text"}`)
		names := toolSearchDescriptorNames(result.Tools)
		require.NotEmpty(t, names)
		assert.Equal(t, "summarize_text", names[0])
		assert.GreaterOrEqual(t, result.TotalMatches, len(names))
	})
}

func TestGeneratedAdapterToolSearchAllocationBudget(t *testing.T) {
	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{MaxResults: 10})

	allocs := testing.AllocsPerRun(20, func() {
		_ = callToolSearch(t, adapter, `{"query":"text analysis keywords sentiment"}`)
	})

	assert.Less(t, allocs, float64(2_000), "tool search should stay within the public adapter allocation budget")
}

func TestGeneratedAdapterToolSearchAcceptsOmittedArguments(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{MaxResults: 2})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name: "search_tools",
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result toolSearchResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	assert.Len(t, result.Tools, 2)
	assert.True(t, result.Truncated)
}

func TestGeneratedAdapterToolSearchRanksBeforeLimiting(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{MaxResults: 1})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"search"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result toolSearchResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	assert.Equal(t, []string{"search"}, toolSearchDescriptorNames(result.Tools))
	assert.True(t, result.Truncated)
}

func TestGeneratedAdapterToolSearchFiltersByCategory(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"category":"knowledge"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result toolSearchResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	assert.Equal(t, []string{"search"}, toolSearchDescriptorNames(result.Tools))
	require.Len(t, result.Tools, 1)
	assert.Equal(t, "knowledge", result.Tools[0].Category)
}

func TestGeneratedAdapterToolSearchFiltersByTags(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"tags":["nlp"]}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result toolSearchResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	assert.Equal(t, []string{"analyze_sentiment", "extract_keywords"}, toolSearchDescriptorNames(result.Tools))
}

func TestGeneratedAdapterToolSearchOmitsSchemasByDefault(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"sentiment"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result toolSearchResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	require.Len(t, result.Tools, 1)
	assert.Empty(t, result.Tools[0].InputSchema)
	assert.Empty(t, result.Tools[0].OutputSchema)
}

func TestGeneratedAdapterToolSearchIncludesSchemasWhenRequested(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"sentiment","include_schemas":true}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result toolSearchResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	require.Len(t, result.Tools, 1)
	assert.NotEmpty(t, result.Tools[0].InputSchema)
	assert.NotEmpty(t, result.Tools[0].OutputSchema)
}

func TestGeneratedAdapterToolSearchRejectsDirectHiddenCallsByDefault(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "analyze_sentiment",
		Arguments: json.RawMessage(`{"text":"great"}`),
	}, stream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Unknown tool")
	assert.Empty(t, stream.events)
}

func TestGeneratedAdapterToolSearchAllowsDirectHiddenCallsWhenCompatEnabled(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{AllowDirectHiddenCalls: true})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "analyze_sentiment",
		Arguments: json.RawMessage(`{"text":"great"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)
	require.Len(t, stream.events[0].Content, 1)
	require.NotNil(t, stream.events[0].Content[0].Text)
	require.JSONEq(t, string(stream.events[0].StructuredContent), *stream.events[0].Content[0].Text)

	var result assistant.AnalyzeSentimentResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	require.NotNil(t, result.Sentiment)
	assert.Equal(t, "positive", *result.Sentiment)
}

func TestGeneratedAdapterToolSearchAlwaysVisibleToolRemainsDirectlyCallable(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{
		AlwaysVisible: []string{"search"},
	})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search",
		Arguments: json.RawMessage(`{"query":"docs"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)
}

func TestGeneratedAdapterToolSearchCallToolInvokesHiddenTool(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
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

func TestGeneratedAdapterToolSearchCallToolInvokesProjectedTool(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "call_tool",
		Arguments: json.RawMessage(`{"name":"projected_lookup_tool","arguments":{"query":"loom"}}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)
	require.Len(t, stream.events[0].Content, 1)
	require.NotNil(t, stream.events[0].Content[0].Text)
	require.JSONEq(t, string(stream.events[0].StructuredContent), *stream.events[0].Content[0].Text)

	var result assistant.ProjectedLookupToolResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	assert.Equal(t, "projected:loom", result.Answer)
	assert.Equal(t, "runtime-toolset", result.Source)
}

func TestGeneratedAdapterToolSearchCallToolInvokesHiddenToolDespiteDirectHiddenGate(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	directStream := &capturedToolsCallStream{}
	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "analyze_sentiment",
		Arguments: json.RawMessage(`{"text":"great"}`),
	}, directStream)
	require.Error(t, err)

	proxyStream := &capturedToolsCallStream{}
	_, err = adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "call_tool",
		Arguments: json.RawMessage(`{"name":"analyze_sentiment","arguments":{"text":"great"}}`),
	}, proxyStream)
	require.NoError(t, err)
	require.Len(t, proxyStream.events, 1)
}

func TestGeneratedAdapterToolSearchRejectsQueryAndPattern(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"sentiment","pattern":"sentiment"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)
	require.NotNil(t, stream.events[0].IsError)
	assert.True(t, *stream.events[0].IsError)
}

func TestGeneratedAdapterToolSearchRejectsInvalidRegex(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"pattern":"["}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)
	require.NotNil(t, stream.events[0].IsError)
	assert.True(t, *stream.events[0].IsError)
}

func TestGeneratedAdapterToolSearchReturnsModelReadableText(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"sentiment"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)
	require.Len(t, stream.events[0].Content, 1)
	require.NotNil(t, stream.events[0].Content[0].Text)
	text := *stream.events[0].Content[0].Text
	assert.Contains(t, text, "Call this tool through call_tool. Do not call analyze_sentiment directly.")
	assert.Contains(t, text, "Tool: call_tool")
	assert.Contains(t, text, `"name": "analyze_sentiment"`)
	assert.Contains(t, text, `"arguments": {`)
	assert.Contains(t, text, `"text": "<string>"`)
}

func TestGeneratedAdapterToolSearchReturnsStructuredDescriptors(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"query":"sentiment"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result toolSearchResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	assert.Equal(t, "sentiment", result.Query)
	assert.Equal(t, 1, result.TotalMatches)
	require.Len(t, result.Tools, 1)
	assert.Equal(t, "analyze_sentiment", result.Tools[0].Name)
	assert.Equal(t, "Analyze Sentiment", result.Tools[0].Title)
	assert.Equal(t, "analysis", result.Tools[0].Category)
	assert.ElementsMatch(t, []string{"sentiment", "nlp"}, result.Tools[0].Tags)
	assert.NotEmpty(t, result.Tools[0].Meta)
	assert.Equal(t, "call_tool", result.Tools[0].CallToolName)
	require.NotEmpty(t, result.Tools[0].CallToolArguments)
	assert.Contains(t, result.Tools[0].CallToolJSON, `"name": "analyze_sentiment"`)
	assert.Contains(t, result.Tools[0].CallToolJSON, `"arguments": {`)
}

func TestGeneratedAdapterToolSearchCallToolSchemaShowsExactWrapperShape(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})

	result, err := adapter.ToolsList(context.Background(), &mcpassistant.ToolsListPayload{})
	require.NoError(t, err)
	require.Len(t, result.Tools, 2)

	callTool := result.Tools[1]
	require.Equal(t, "call_tool", callTool.Name)
	schemaJSON, err := json.Marshal(callTool.InputSchema)
	require.NoError(t, err)
	schema := string(schemaJSON)
	assert.Contains(t, schema, `"required":["name","arguments"]`)
	assert.Contains(t, schema, "Exact discovered tool name")
	assert.Contains(t, schema, "Use {} when the discovered tool takes no arguments")
	assert.Contains(t, schema, "Do not use args")
}

func TestGeneratedAdapterToolSearchStructuredContentIncludesPattern(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"pattern":"keyword.*"}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)

	var result toolSearchResult
	require.NoError(t, json.Unmarshal(stream.events[0].StructuredContent, &result))
	assert.Equal(t, "keyword.*", result.Pattern)
	assert.Contains(t, toolSearchDescriptorNames(result.Tools), "extract_keywords")
}

func TestGeneratedAdapterToolSearchSyntheticToolHasOutputSchema(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})

	result, err := adapter.ToolsList(context.Background(), &mcpassistant.ToolsListPayload{})
	require.NoError(t, err)
	require.NotEmpty(t, result.Tools)
	require.Equal(t, "search_tools", result.Tools[0].Name)
	assert.NotNil(t, result.Tools[0].OutputSchema)
}

func TestGeneratedAdapterToolSearchCallToolRejectsSyntheticTools(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "call_tool",
		Arguments: json.RawMessage(`{"name":"call_tool","arguments":{"name":"search"}}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)
	require.NotNil(t, stream.events[0].IsError)
	assert.True(t, *stream.events[0].IsError)
}

func TestGeneratedAdapterToolSearchProxyPreservesToolContext(t *testing.T) {
	t.Parallel()

	var seen []string
	adapter := mcpassistant.NewMCPAdapter(NewAssistant(), promptProvider{}, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{},
		ToolCallInterceptors: []mcpassistant.ToolCallInterceptor{
			func(ctx context.Context, info mcpassistant.ToolCallInterceptorInfo, payload *mcpassistant.ToolsCallPayload, stream mcpassistant.ToolsCallServerStream, next mcpassistant.ToolCallHandler) (bool, error) {
				seen = append(seen, info.Tool())
				return next(ctx, payload, stream)
			},
		},
	})
	_, err := adapter.Initialize(context.Background(), &mcpassistant.InitializePayload{ProtocolVersion: "2025-06-18"})
	require.NoError(t, err)
	stream := &capturedToolsCallStream{}

	_, err = adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "call_tool",
		Arguments: json.RawMessage(`{"name":"analyze_sentiment","arguments":{"text":"great"}}`),
	}, stream)
	require.NoError(t, err)
	assert.Equal(t, []string{"call_tool", "analyze_sentiment"}, seen)
}

func BenchmarkGeneratedAdapterToolSearch(b *testing.B) {
	adapter := newToolSearchAdapter(b, &mcpassistant.ToolSearchOptions{MaxResults: 10})
	queries := []string{
		"summarize_text",
		"summarize text",
		"Search Knowledge Base",
		"document lookup",
		"sumarize txt",
		"text analysis keywords sentiment",
	}

	b.ReportAllocs()
	for b.Loop() {
		for _, query := range queries {
			_ = callToolSearch(b, adapter, `{"query":`+strconv.Quote(query)+`}`)
		}
	}
}

func TestGeneratedAdapterToolSearchProxyPreservesValidationErrors(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "call_tool",
		Arguments: json.RawMessage(`{"name":"analyze_sentiment","arguments":{}}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)
	require.NotNil(t, stream.events[0].IsError)
	assert.True(t, *stream.events[0].IsError)
	require.Len(t, stream.events[0].Content, 1)
	require.NotNil(t, stream.events[0].Content[0].Text)
	assert.Contains(t, *stream.events[0].Content[0].Text, "text")
}

func TestGeneratedAdapterToolSearchProxyPreservesErrorMapping(t *testing.T) {
	t.Parallel()

	adapter := mcpassistant.NewMCPAdapter(toolErrorAssistant{
		Service: NewAssistant(),
		err:     errors.New("backend detail"),
	}, promptProvider{}, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{},
		ErrorMapper: func(error) error {
			return loom.PermanentError("mapped_error", "mapped safe message")
		},
	})
	_, err := adapter.Initialize(context.Background(), &mcpassistant.InitializePayload{ProtocolVersion: "2025-06-18"})
	require.NoError(t, err)
	stream := &capturedToolsCallStream{}

	_, err = adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "call_tool",
		Arguments: json.RawMessage(`{"name":"analyze_sentiment","arguments":{"text":"bad"}}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)
	require.Len(t, stream.events[0].Content, 1)
	require.NotNil(t, stream.events[0].Content[0].Text)
	assert.Contains(t, *stream.events[0].Content[0].Text, "mapped safe message")
}

func TestGeneratedAdapterToolSearchProxyRecordsProxyAndTargetTelemetryAttributes(t *testing.T) {
	t.Parallel()

	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	adapter := mcpassistant.NewMCPAdapter(NewAssistant(), promptProvider{}, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{},
		Tracer:     tracerProvider.Tracer("tool-search-test"),
	})
	_, err := adapter.Initialize(context.Background(), &mcpassistant.InitializePayload{ProtocolVersion: "2025-06-18"})
	require.NoError(t, err)
	stream := &capturedToolsCallStream{}

	_, err = adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "call_tool",
		Arguments: json.RawMessage(`{"name":"analyze_sentiment","arguments":{"text":"great"}}`),
	}, stream)
	require.NoError(t, err)

	var found bool
	for _, span := range spanRecorder.Ended() {
		if span.Name() != "mcp.tools/call" {
			continue
		}
		attrs := toolSearchSpanAttrs(span.Attributes())
		if attrs["mcp.tool"].AsString() == "call_tool" && attrs["mcp.target_tool"].AsString() == "analyze_sentiment" {
			found = true
		}
	}
	assert.True(t, found)
}

func TestGeneratedAdapterToolSearchProxyPreservesRequestContext(t *testing.T) {
	t.Parallel()

	const contextKey = "tool-search-context"
	var observed bool
	adapter := mcpassistant.NewMCPAdapter(NewAssistant(), promptProvider{}, &mcpassistant.MCPAdapterOptions{
		ToolSearch: &mcpassistant.ToolSearchOptions{},
		ToolCallInterceptors: []mcpassistant.ToolCallInterceptor{
			func(ctx context.Context, info mcpassistant.ToolCallInterceptorInfo, payload *mcpassistant.ToolsCallPayload, stream mcpassistant.ToolsCallServerStream, next mcpassistant.ToolCallHandler) (bool, error) {
				if info.Tool() == "analyze_sentiment" && ctx.Value(contextKey) == "preserved" {
					observed = true
				}
				return next(ctx, payload, stream)
			},
		},
	})
	_, err := adapter.Initialize(context.Background(), &mcpassistant.InitializePayload{ProtocolVersion: "2025-06-18"})
	require.NoError(t, err)
	ctx := context.WithValue(context.Background(), contextKey, "preserved")
	stream := &capturedToolsCallStream{}

	_, err = adapter.ToolsCall(ctx, &mcpassistant.ToolsCallPayload{
		Name:      "call_tool",
		Arguments: json.RawMessage(`{"name":"analyze_sentiment","arguments":{"text":"great"}}`),
	}, stream)
	require.NoError(t, err)
	assert.True(t, observed)
}

func toolSearchSpanAttrs(attrs []attribute.KeyValue) map[string]attribute.Value {
	values := make(map[string]attribute.Value, len(attrs))
	for _, attr := range attrs {
		values[string(attr.Key)] = attr.Value
	}
	return values
}

func TestGeneratedAdapterToolSearchResultTextMentionsTruncation(t *testing.T) {
	t.Parallel()

	adapter := newToolSearchAdapter(t, &mcpassistant.ToolSearchOptions{MaxResults: 1})
	stream := &capturedToolsCallStream{}

	_, err := adapter.ToolsCall(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "search_tools",
		Arguments: json.RawMessage(`{"tags":["nlp"]}`),
	}, stream)
	require.NoError(t, err)
	require.Len(t, stream.events, 1)
	require.Len(t, stream.events[0].Content, 1)
	require.NotNil(t, stream.events[0].Content[0].Text)
	assert.True(t, strings.Contains(*stream.events[0].Content[0].Text, "Found 1 of 2"))
}
