package codegen

import (
	"path/filepath"
	"testing"

	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	gcodegen "github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

// generateTransportConformanceFixture generates an SDK-only service exposing
// tools, resources, and prompts. The resource is watchable so the generated
// SDK server must also own the subscription lifecycle.
func generateTransportConformanceFixture(t *testing.T) []*gcodegen.File {
	t.Helper()
	restore := resetMCPCodegenState(t)
	t.Cleanup(restore)

	svc, methods := testService("assistant", "search", "read_document", "build_summary")
	methods["search"].Payload = &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "query", Attribute: &expr.AttributeExpr{Type: expr.String}},
		},
		Validation: &expr.ValidationExpr{Required: []string{"query"}},
	}
	methods["search"].Stream = expr.ServerStreamKind
	methods["search"].StreamingResult = &expr.AttributeExpr{Type: expr.String}
	methods["read_document"].Payload = testResourceQueryPayload()
	root := testRootExpr([]*expr.ServiceExpr{svc}, nil)
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "search", Method: methods["search"]},
		},
		Resources: []*mcpexpr.ResourceExpr{
			{Name: "documents", URI: "doc://list", Method: methods["read_document"], Watchable: true},
		},
		Prompts: []*mcpexpr.PromptExpr{
			{
				Name: "summarize",
				Messages: []*mcpexpr.MessageExpr{
					{Role: "user", Content: "Summarize"},
				},
			},
		},
	})

	mcpexpr.Root.RegisterDynamicPrompt(svc, &mcpexpr.DynamicPromptExpr{Name: "dynamic_summary", Method: methods["build_summary"]})

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)
	return files
}

func TestGenerateTransportConformance(t *testing.T) {
	files := generateTransportConformanceFixture(t)

	t.Run("official SDK is the sole wire transport", func(t *testing.T) {
		paths := make([]string, 0, len(files))
		for _, file := range files {
			path := filepath.ToSlash(file.Path)
			paths = append(paths, path)
			require.NotContains(t, path, "/jsonrpc/mcp_assistant/")
			require.NotContains(t, path, "/adapter/client/")
			require.NotContains(t, path, "protocol_version.go")
			require.NotContains(t, path, "endpoints.go")
			require.NotContains(t, path, "client.go")
			require.NotContains(t, path, "stream.go")
			require.NotContains(t, path, "sse.go")
		}
		require.Contains(t, paths, "gen/mcp_assistant/sdk_server.go")
	})

	t.Run("streaming Loom methods aggregate behind a unary MCP boundary", func(t *testing.T) {
		service := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_assistant/service.go"))
		adapter := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_assistant/adapter_server.go"))
		sdk := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_assistant/sdk_server.go"))

		require.Contains(t, service, "ToolsCall(context.Context, *ToolsCallPayload) (res *ToolsCallResult, err error)")
		require.NotContains(t, service, "ToolsCallServerStream")
		require.Contains(t, adapter, "type toolCallResultCollector struct")
		require.Contains(t, adapter, "func (a *MCPAdapter) ToolsCall(ctx context.Context, p *ToolsCallPayload) (res *ToolsCallResult, err error)")
		require.Contains(t, sdk, "result, err := a.ToolsCall(ctx, payload)")
		require.NotContains(t, sdk, "sdkToolCallCollector")
	})

	t.Run("SDK owns sessions subscriptions progress and elicitation", func(t *testing.T) {
		sdk := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_assistant/sdk_server.go"))

		require.Contains(t, sdk, "sdkbridge.NewServer(sdkbridge.Config{")
		require.Contains(t, sdk, "WatchableResource: func(uri string) bool")
		require.Contains(t, sdk, "func (s *SDKServer) ResourceUpdated(ctx context.Context, uri string) error")
		require.Contains(t, sdk, "sdkbridge.ToolHandler(")
		require.Contains(t, sdk, "sdkbridge.HandlerContext{")
		require.Contains(t, sdk, "RequestStateKey")
		require.NotContains(t, sdk, "sdkclient.WithClientFeatures")
		require.NotContains(t, sdk, "sdkclient.InputRequired")
		require.NotContains(t, sdk, "serveSDKEventsStream")
		require.NotContains(t, sdk, "writeSDKNotificationEvent")
	})

	t.Run("adapter preserves input-required control flow", func(t *testing.T) {
		adapter := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_assistant/adapter_server.go"))
		require.Contains(t, adapter, "if mcpruntime.IsInputRequired(err)")
		require.Contains(t, adapter, "mcpruntime.IsInvalidClientInput(err)")
		require.Contains(t, adapter, "return err")
	})

	t.Run("resource queries preserve schema shape through typed bridge descriptors", func(t *testing.T) {
		adapter := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_assistant/adapter_server.go"))
		sdk := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_assistant/sdk_server.go"))

		require.Contains(t, adapter, "sdkbridge.ResourceQueryJSONTyped(request.URI, map[string]mcpruntime.QueryField{")
		require.Contains(t, adapter, `"cursor"`)
		require.Contains(t, adapter, "String: true")
		require.Contains(t, adapter, `"tags"`)
		require.Contains(t, adapter, "Repeated: true")
		require.Contains(t, sdk, "Template: &mcpsdk.ResourceTemplate{")
		require.Contains(t, sdk, `URITemplate: "doc://list{?cursor,enabled,limit,offset,ratio,tags*,tenant}"`)
		require.Contains(t, sdk, "[]sdkbridge.ResourceURIMatcher{")
		require.Contains(t, sdk, `uritemplate.MustNew("doc://list{?cursor,enabled,limit,offset,ratio,tags*,tenant}").Regexp()`)
		require.Contains(t, sdk, `"limit"`)
	})
}
