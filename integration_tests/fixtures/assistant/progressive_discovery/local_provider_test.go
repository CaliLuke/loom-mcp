package progressivediscovery

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpcatalog "example.com/assistant/progressive_discovery/gen/mcp_catalog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedLocalProgressiveDiscoveryProviderMatchesSDKServer(t *testing.T) {
	t.Parallel()

	toolSearch := func() *mcpcatalog.ToolSearchOptions {
		return &mcpcatalog.ToolSearchOptions{
			AlwaysVisible:  []string{"lookup"},
			SearchToolName: "discover",
			CallToolName:   "invoke",
		}
	}

	publicServer, err := mcpcatalog.NewSDKServer(NewCatalog(), &mcpcatalog.SDKServerOptions{
		Adapter: &mcpcatalog.MCPAdapterOptions{ToolSearch: toolSearch()},
	})
	require.NoError(t, err)
	httpServer := httptest.NewServer(publicServer.Handler)
	t.Cleanup(httpServer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "progressive-provider-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint:             httpServer.URL,
		HTTPClient:           &http.Client{Transport: transport, Timeout: 10 * time.Second},
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, session.Close())
	})

	localAdapter := mcpcatalog.NewMCPAdapter(NewCatalog(), &mcpcatalog.MCPAdapterOptions{ToolSearch: toolSearch()})
	local, err := mcpcatalog.NewCatalogCatalogMcpLocalToolsetRegistration(localAdapter)
	require.NoError(t, err)
	runtime := agentsruntime.New()
	require.NoError(t, runtime.RegisterToolset(local))
	assert.Contains(t, runtime.ListToolsets(), "catalog.catalog-mcp")

	t.Run("compact catalog", func(t *testing.T) {
		public, err := session.ListTools(ctx, nil)
		require.NoError(t, err)
		publicByName := make(map[string]*sdkmcp.Tool, len(public.Tools))
		for _, tool := range public.Tools {
			publicByName[tool.Name] = tool
		}
		assert.ElementsMatch(t, []string{"discover", "invoke", "lookup"}, mapKeys(publicByName))
		assert.ElementsMatch(t, mapKeys(publicByName), localSpecNames(local.Specs))
		for _, spec := range local.Specs {
			publicTool := publicByName[string(spec.Name)]
			require.NotNil(t, publicTool)
			publicSchema, err := json.Marshal(publicTool.InputSchema)
			require.NoError(t, err)
			assert.JSONEq(t, string(publicSchema), string(spec.Payload.Schema))
		}
	})

	t.Run("always-visible invocation", func(t *testing.T) {
		arguments := map[string]any{"query": "loom"}
		public := callPublicTool(t, ctx, session, "lookup", arguments)
		localResult := callLocalTool(t, ctx, local, "lookup", arguments)
		assert.Equal(t, map[string]any{"value": "direct:loom"}, public.StructuredContent)
		assert.Equal(t, public.StructuredContent, localResult.Result)
	})

	t.Run("search parity", func(t *testing.T) {
		arguments := map[string]any{"query": "projected lookup", "include_schemas": true}
		public := callPublicTool(t, ctx, session, "discover", arguments)
		localResult := callLocalTool(t, ctx, local, "discover", arguments)
		assert.False(t, public.IsError)
		require.Nil(t, localResult.Error)
		assert.Equal(t, public.StructuredContent, localResult.Result)
	})

	t.Run("hidden direct invocation", func(t *testing.T) {
		arguments := map[string]any{"name": "lookup", "arguments": map[string]any{"query": "loom"}}
		public := callPublicTool(t, ctx, session, "invoke", arguments)
		localResult := callLocalTool(t, ctx, local, "invoke", arguments)
		assert.Equal(t, map[string]any{"value": "direct:loom"}, public.StructuredContent)
		assert.Equal(t, public.StructuredContent, localResult.Result)
	})

	t.Run("projected invocation", func(t *testing.T) {
		arguments := map[string]any{"name": "projected_lookup", "arguments": map[string]any{"query": "loom"}}
		public := callPublicTool(t, ctx, session, "invoke", arguments)
		localResult := callLocalTool(t, ctx, local, "invoke", arguments)
		assert.Equal(t, map[string]any{"value": "projected:loom"}, public.StructuredContent)
		assert.Equal(t, public.StructuredContent, localResult.Result)
	})

	t.Run("invalid arguments", func(t *testing.T) {
		arguments := map[string]any{"name": "lookup", "arguments": map[string]any{}}
		public := callPublicTool(t, ctx, session, "invoke", arguments)
		localResult := callLocalTool(t, ctx, local, "invoke", arguments)
		assert.True(t, public.IsError)
		require.Error(t, localResult.Error)
		assert.Contains(t, localResult.Error.Error(), "query")
	})

	t.Run("unknown tool", func(t *testing.T) {
		arguments := map[string]any{"name": "missing", "arguments": map[string]any{}}
		public := callPublicTool(t, ctx, session, "invoke", arguments)
		localResult := callLocalTool(t, ctx, local, "invoke", arguments)
		assert.True(t, public.IsError)
		require.Error(t, localResult.Error)
		assert.Contains(t, localResult.Error.Error(), "missing")
	})
}

func callPublicTool(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession, name string, arguments map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: arguments})
	require.NoError(t, err)
	return result
}

func callLocalTool(t *testing.T, ctx context.Context, registration agentsruntime.ToolsetRegistration, name string, arguments map[string]any) *planner.ToolResult {
	t.Helper()
	payload, err := json.Marshal(arguments)
	require.NoError(t, err)
	result, err := registration.Execute(ctx, &planner.ToolRequest{
		Name:       tools.Ident("catalog.catalog-mcp." + name),
		Payload:    rawjson.Message(payload),
		ToolCallID: "local-call",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ToolResult)
	return result.ToolResult
}

func localSpecNames(specs []tools.ToolSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, string(spec.Name))
	}
	return names
}

func mapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
