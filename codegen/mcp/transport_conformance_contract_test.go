package codegen

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	mcpexpr "github.com/CaliLuke/loom-mcp/expr/mcp"
	gcodegen "github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

// generateTransportConformanceFixture generates a service exposing tools,
// resources, and prompts so every MCP list decoder and both server streams are
// present in the JSON-RPC output.
func generateTransportConformanceFixture(t *testing.T) []*gcodegen.File {
	t.Helper()
	restore := resetMCPCodegenState(t)
	t.Cleanup(restore)

	svc, methods := testService("assistant", "search", "read_document")
	methods["search"].Payload = &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "query", Attribute: &expr.AttributeExpr{Type: expr.String}},
		},
		Validation: &expr.ValidationExpr{Required: []string{"query"}},
	}
	methods["read_document"].Payload = testResourceQueryPayload()
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{
		jsonrpcService(svc, "/rpc"),
	})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant-mcp",
		Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{
			{Name: "search", Method: methods["search"]},
		},
		Resources: []*mcpexpr.ResourceExpr{
			{Name: "documents", URI: "doc://list", Method: methods["read_document"]},
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

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)
	return files
}

func TestGenerate_ListDecodersAcceptOmittedParams(t *testing.T) {
	files := generateTransportConformanceFixture(t)
	file := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "jsonrpc", "mcp_assistant", "server", "encode_decode.go"))
	rendered := renderGeneratedFile(t, file)

	// All-optional list params must decode absence as {} (spec-optional params).
	require.Contains(t, rendered, "return NewToolsListPayload(&body), nil")
	require.Contains(t, rendered, "return NewResourcesListPayload(&body), nil")
	require.Contains(t, rendered, "return NewPromptsListPayload(&body), nil")

	// Methods with required params keep the missing payload error mapping.
	require.Contains(t, rendered, "loom.MissingPayloadError()")
	toolsCall := decoderFunctionBody(t, rendered, "DecodeToolsCallRequest")
	require.Contains(t, toolsCall, "loom.MissingPayloadError()")
	toolsList := decoderFunctionBody(t, rendered, "DecodeToolsListRequest")
	require.NotContains(t, toolsList, "loom.MissingPayloadError()")
}

func TestGenerate_StreamFinalResponseUsesMessageEvent(t *testing.T) {
	files := generateTransportConformanceFixture(t)
	for _, generated := range files {
		require.NotEqual(t, filepath.Join(gcodegen.Gendir, "jsonrpc", "mcp_assistant", "server", "sse.go"), generated.Path,
			"mixed MCP transports must not retain the unreachable standalone SSE implementation")
	}
	file := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "jsonrpc", "mcp_assistant", "server", "stream.go"))
	rendered := renderGeneratedFile(t, file)

	require.NotContains(t, rendered, `sendSSEEvent("response", `,
		"final SSE responses must use the default \"message\" event so conformant clients process them")
	require.Contains(t, rendered, `sendSSEEvent("message", `)
	require.Contains(t, rendered, `fmt.Fprintf(s.w, "id: %s\ndata:\n\n", mcpruntime.NewSessionID())`)
	require.Equal(t, 2, strings.Count(rendered, `fmt.Fprint(s.w, "event: retry\nretry: 1000\ndata:\n\n")`),
		"each generated endpoint stream must publish the reconnect delay")

	serverFile := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "jsonrpc", "mcp_assistant", "server", "server.go"))
	server := renderGeneratedFile(t, serverFile)
	require.NotContains(t, server, "func (s *Server) handleSSE(")
	require.Equal(t, 1, strings.Count(server, `r.Method == http.MethodGet && req.Method == "events/stream"`),
		"only the live events/stream handler may open a GET stream")

	adapterFile := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "adapter_server.go"))
	adapter := renderGeneratedFile(t, adapterFile)
	require.NotContains(t, adapter, "subs map[string]int")
	require.NotContains(t, adapter, "subsMu sync.Mutex")
}

func TestGenerate_IntermediateStreamsUseNamespacedNotificationMethod(t *testing.T) {
	files := generateTransportConformanceFixture(t)
	file := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "jsonrpc", "mcp_assistant", "server", "stream.go"))
	rendered := renderGeneratedFile(t, file)

	require.Contains(t, rendered, `"method":  "mcp_assistant/stream.event"`)
	require.NotContains(t, rendered, `"method":  "tools/call"`)
}

func TestGenerate_WatchableResourcesRetainSubscriptionRegistry(t *testing.T) {
	var rendered bytes.Buffer
	err := adapterCoreSection(&AdapterData{
		Package:               "servicepkg",
		HasWatchableResources: true,
	}).Write(&rendered)
	require.NoError(t, err)
	require.Contains(t, rendered.String(), "subs   map[string]int")
	require.Contains(t, rendered.String(), "subsMu sync.Mutex")
	require.Contains(t, rendered.String(), "subs: make(map[string]int)")
}

func TestGenerate_JSONRPCMountValidatesOrigin(t *testing.T) {
	files := generateTransportConformanceFixture(t)
	file := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "jsonrpc", "mcp_assistant", "server", "server.go"))
	rendered := renderGeneratedFile(t, file)

	require.Contains(t, rendered, "var MCPCrossOriginProtection = http.NewCrossOriginProtection()")
	require.Contains(t, rendered, "MCPCrossOriginProtection.Check(r)")
	require.Contains(t, rendered, `http.Error(w, "origin not allowed", http.StatusForbidden)`)
}

func TestGenerate_JSONRPCClientSendsAcceptAndSession(t *testing.T) {
	files := generateTransportConformanceFixture(t)
	file := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "jsonrpc", "mcp_assistant", "client", "client.go"))
	rendered := renderGeneratedFile(t, file)

	require.Contains(t, rendered, "doer = &mcpClientDoer{next: doer}")
	require.Contains(t, rendered, `req.Header.Set("Accept", "application/json, text/event-stream")`)
	require.NotContains(t, rendered, `req.Header.Set("Accept", "text/event-stream")`)
	require.Contains(t, rendered, `resp.Header.Get("Mcp-Session-Id")`)
	require.Contains(t, rendered, `req.Header.Set("Mcp-Session-Id", sessionID)`)
	require.Contains(t, rendered, `req.Header.Set("MCP-Protocol-Version", protocolVersion)`)
	require.Contains(t, rendered, "func mcpNegotiatedProtocolVersion(resp *http.Response) string")
}

func TestGenerate_SDKServerDoesNotAdvertiseDeadEventsCapability(t *testing.T) {
	files := generateTransportConformanceFixture(t)
	file := findGeneratedFile(t, files, filepath.Join(gcodegen.Gendir, "mcp_assistant", "sdk_server.go"))
	rendered := renderGeneratedFile(t, file)

	require.NotContains(t, rendered, "serveSDKEventsStream")
	require.NotContains(t, rendered, "writeSDKNotificationEvent")
	require.NotContains(t, rendered, "sdkEventsStreamParams")
	require.NotContains(t, rendered, "sdkSessionByID")
	require.NotContains(t, rendered, `"loom-mcp"`,
		"SDK mode must not advertise the events/stream capability: the SDK transport owns GET and the handler is unreachable")
	require.Contains(t, rendered, "sdkServerOptionsWithDefaults")
	require.Contains(t, rendered, "sdkclient.WithClientFeatures(ctx, serverSession)")
	require.Contains(t, rendered, "func (w *sdkResponseObserver) Unwrap() http.ResponseWriter")
	require.NotContains(t, rendered, "sdkSessionElicitor")
}

// decoderFunctionBody extracts the body of one generated top-level function so
// assertions do not accidentally match sibling decoders.
func decoderFunctionBody(t *testing.T, source, name string) string {
	t.Helper()
	start := strings.Index(source, "func "+name+"(")
	require.GreaterOrEqual(t, start, 0, "function %s not found", name)
	rest := source[start:]
	if end := strings.Index(rest, "\n}\n"); end >= 0 {
		return rest[:end+3]
	}
	return rest
}
