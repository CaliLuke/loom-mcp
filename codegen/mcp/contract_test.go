package codegen

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	gcodegen "github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

func TestGenerateRequiresEveryServiceMethodToMapToMCP(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("calc", "add", "subtract")
	root := testRootExpr([]*expr.ServiceExpr{svc}, nil)
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "calc",
		Version: "1.0.0",
		Tools:   []*mcpexpr.ToolExpr{{Name: "add", Method: methods["add"]}},
	})

	_, err := Generate("example.com/calc/gen", []eval.Root{root}, nil)

	require.ErrorContains(t, err, `service "calc" has methods not mapped to MCP constructs: subtract`)
}

func TestGenerateSDKOnlyWithoutJSONRPCDesign(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("demo", "ping")
	root := testRootExpr([]*expr.ServiceExpr{svc}, nil)
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "demo",
		Version: "0.1.0",
		Tools:   []*mcpexpr.ToolExpr{{Name: "ping", Method: methods["ping"]}},
	})

	files, err := Generate("example.com/demo/gen", []eval.Root{root}, nil)
	require.NoError(t, err)

	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, filepath.ToSlash(file.Path))
	}
	require.Contains(t, paths, "gen/mcp_demo/service.go")
	require.Contains(t, paths, "gen/mcp_demo/adapter_server.go")
	require.Contains(t, paths, "gen/mcp_demo/sdk_server.go")
	for _, path := range paths {
		require.NotContains(t, path, "gen/jsonrpc/mcp_demo/")
		require.NotContains(t, path, "/adapter/client/")
		require.NotEqual(t, "gen/mcp_demo/protocol_version.go", path)
		require.NotEqual(t, "gen/mcp_demo/endpoints.go", path)
		require.NotEqual(t, "gen/mcp_demo/client.go", path)
	}
}

func TestPrepareServicesLeavesApplicationTransportsUntouched(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("demo", "ping")
	jsonrpc := jsonrpcService(svc, "/application-rpc")
	root := testRootExpr([]*expr.ServiceExpr{svc}, []*expr.HTTPServiceExpr{jsonrpc})
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "demo",
		Version: "0.1.0",
		Tools:   []*mcpexpr.ToolExpr{{Name: "ping", Method: methods["ping"]}},
	})

	require.NoError(t, PrepareServices("", []eval.Root{root}))
	require.Len(t, root.API.HTTP.Services, 1)
	require.Equal(t, jsonrpc, root.API.JSONRPC.Services[0])
	require.Empty(t, jsonrpc.HTTPEndpoints)
	require.Nil(t, methods["ping"].Meta["jsonrpc"])
}

func TestGenerateSDKServerAlwaysExposesOptionalRuntimeCORS(t *testing.T) {
	files := generateToolDiscoveryFixture(t)
	sdk := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_assistant/sdk_server.go"))

	require.Contains(t, sdk, "RuntimeCORS       *loomhttp.RuntimeCORSPolicy")
	require.NotContains(t, sdk, "runtime CORS policy is required")
	require.Contains(t, sdk, "if runtimeCORS != nil")
	require.Contains(t, sdk, "handler = sdkRuntimeCORSHandler(handler, *runtimeCORS)")
	require.Contains(t, sdk, "CrossOriginProtection: http.NewCrossOriginProtection()")
}

func TestGenerateAdapterUsesUnaryToolBoundaryAndPrivateStreamingCollector(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("assistant", "search")
	methods["search"].Stream = expr.ServerStreamKind
	methods["search"].StreamingResult = &expr.AttributeExpr{Type: expr.String}
	root := testRootExpr([]*expr.ServiceExpr{svc}, nil)
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "assistant",
		Version: "1.0.0",
		Tools:   []*mcpexpr.ToolExpr{{Name: "search", Method: methods["search"]}},
	})

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)
	service := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_assistant/service.go"))
	adapter := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_assistant/adapter_server.go"))
	sdk := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_assistant/sdk_server.go"))

	require.Contains(t, service, "ToolsCall(context.Context, *ToolsCallPayload) (res *ToolsCallResult, err error)")
	require.NotContains(t, service, "ToolsCallServerStream")
	require.Contains(t, service, "Arguments loom.Nullable[any]")
	require.Contains(t, service, "StructuredContent loom.Nullable[any]")
	require.NotContains(t, service, "Arguments json.RawMessage")
	require.NotContains(t, service, "StructuredContent json.RawMessage")
	require.Contains(t, adapter, "type toolCallResultCollector struct")
	require.Contains(t, adapter, "type SearchStreamBridge struct")
	require.Contains(t, adapter, "ToolCallHandler func(ctx context.Context, payload *ToolsCallPayload) (*ToolsCallResult, error)")
	require.Contains(t, adapter, "ToolCallInterceptor func(ctx context.Context, info ToolCallInterceptorInfo, payload *ToolsCallPayload, next ToolCallHandler) (*ToolsCallResult, error)")
	require.NotContains(t, adapter, "ToolCallInterceptor func(ctx context.Context, info ToolCallInterceptorInfo, payload *ToolsCallPayload, stream")
	require.Contains(t, adapter, "func mcpJSONRaw(value loom.Nullable[any]) (json.RawMessage, error)")
	require.Contains(t, adapter, "if raw, ok := actual.(json.RawMessage); ok")
	require.Contains(t, adapter, "func mcpJSONFromRaw(raw json.RawMessage) loom.Nullable[any]")
	require.Contains(t, adapter, "func (a *MCPAdapter) ToolsCall(ctx context.Context, p *ToolsCallPayload) (res *ToolsCallResult, err error)")
	require.Contains(t, sdk, "result, err := a.ToolsCall(ctx, payload)")
	require.Contains(t, sdk, "payload.Arguments = mcpJSONFromRaw(req.Params.Arguments)")
	require.Contains(t, sdk, "structuredContent, err := mcpJSONRaw(result.StructuredContent)")
	require.NotContains(t, sdk, "sdkToolCallCollector")
}

func TestGenerateStreamingResourceUsesPrivateUnaryCollector(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("catalog", "watch")
	methods["watch"].Stream = expr.ServerStreamKind
	methods["watch"].StreamingResult = &expr.AttributeExpr{Type: expr.String}
	root := testRootExpr([]*expr.ServiceExpr{svc}, nil)
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:      "catalog",
		Version:   "1.0.0",
		Resources: []*mcpexpr.ResourceExpr{{Name: "feed", URI: "feed://current", MimeType: "application/json", Method: methods["watch"]}},
	})

	files, err := Generate("example.com/catalog/gen", []eval.Root{root}, nil)
	require.NoError(t, err)
	service := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_catalog/service.go"))
	adapter := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_catalog/adapter_server.go"))

	require.Contains(t, service, "ResourcesRead(context.Context, *ResourcesReadPayload) (res *ResourcesReadResult, err error)")
	require.NotContains(t, service, "ResourcesReadServerStream")
	require.Contains(t, adapter, "type WatchResourceStreamCollector struct")
	require.Contains(t, adapter, "Contents: collector.contents")
}

func TestGenerateWatchableResourcesUseSDKSubscriptions(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("catalog", "status")
	root := testRootExpr([]*expr.ServiceExpr{svc}, nil)
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name:    "catalog",
		Version: "1.0.0",
		Resources: []*mcpexpr.ResourceExpr{{
			Name: "status", URI: "status://current", MimeType: "application/json", Method: methods["status"], Watchable: true,
		}},
	})

	files, err := Generate("example.com/catalog/gen", []eval.Root{root}, nil)
	require.NoError(t, err)
	sdk := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_catalog/sdk_server.go"))
	adapter := renderGeneratedFile(t, findGeneratedFile(t, files, "gen/mcp_catalog/adapter_server.go"))

	require.Contains(t, sdk, "watchable MCP resources require stateful Streamable HTTP sessions")
	require.Contains(t, sdk, "opts.SubscribeHandler = func(ctx context.Context, req *mcpsdk.SubscribeRequest) error")
	require.Contains(t, sdk, "opts.UnsubscribeHandler = func(ctx context.Context, req *mcpsdk.UnsubscribeRequest) error")
	require.Contains(t, sdk, `case "status://current":`)
	require.Contains(t, sdk, "func (s *SDKServer) ResourceUpdated(ctx context.Context, uri string) error")
	require.Contains(t, sdk, "s.Server.ResourceUpdated(ctx, &mcpsdk.ResourceUpdatedNotificationParams{URI: uri})")
	require.NotContains(t, adapter, "subs map[string]int")
	require.NotContains(t, adapter, "EventsStream")
}

func TestPrepareServicesRejectsUnsupportedResourceQueryField(t *testing.T) {
	restore := resetMCPCodegenState(t)
	defer restore()

	svc, methods := testService("catalog", "lookup")
	methods["lookup"].Payload = &expr.AttributeExpr{Type: &expr.Object{{
		Name: "filter", Attribute: &expr.AttributeExpr{Type: &expr.Map{KeyType: &expr.AttributeExpr{Type: expr.String}, ElemType: &expr.AttributeExpr{Type: expr.String}}},
	}}}
	root := testRootExpr([]*expr.ServiceExpr{svc}, nil)
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name: "catalog", Version: "1.0.0",
		Resources: []*mcpexpr.ResourceExpr{{Name: "lookup", URI: "catalog://lookup", MimeType: "application/json", Method: methods["lookup"]}},
	})

	err := PrepareServices("", []eval.Root{root})
	require.ErrorContains(t, err, "incompatible resource query payload")
}

func resetMCPCodegenState(t *testing.T) func() {
	t.Helper()

	previousRoot := mcpexpr.Root
	mcpexpr.Root = mcpexpr.NewRoot()

	return func() {
		mcpexpr.Root = previousRoot
	}
}

func generateToolDiscoveryFixture(t *testing.T) []*gcodegen.File {
	t.Helper()
	restore := resetMCPCodegenState(t)
	t.Cleanup(restore)

	svc, methods := testService("assistant", "search")
	methods["search"].Payload = &expr.AttributeExpr{
		Type:       &expr.Object{{Name: "query", Attribute: &expr.AttributeExpr{Type: expr.String, Description: "Search query"}}},
		Validation: &expr.ValidationExpr{Required: []string{"query"}},
	}
	methods["search"].Result = &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "summary", Attribute: &expr.AttributeExpr{Type: expr.String, Description: "Search summary"}},
			{Name: "score", Attribute: &expr.AttributeExpr{Type: expr.Float64, Description: "Search score"}},
		},
		Validation: &expr.ValidationExpr{Required: []string{"summary"}},
	}
	root := testRootExpr([]*expr.ServiceExpr{svc}, nil)
	mcpexpr.Root.RegisterMCP(svc, &mcpexpr.MCPExpr{
		Name: "assistant-mcp", Version: "1.0.0",
		Tools: []*mcpexpr.ToolExpr{{
			Name: "search", Description: "Search indexed content", Title: "Search Content",
			DiscoveryCategory: "knowledge", DiscoveryTags: []string{"search", "retrieval"},
			DiscoveryKeywords: []string{"lookup", "documents"}, Method: methods["search"],
		}},
	})

	files, err := Generate("example.com/assistant/gen", []eval.Root{root}, nil)
	require.NoError(t, err)
	return files
}

func testService(name string, methodNames ...string) (*expr.ServiceExpr, map[string]*expr.MethodExpr) {
	svc := &expr.ServiceExpr{Name: name}
	methods := make(map[string]*expr.MethodExpr, len(methodNames))
	for _, methodName := range methodNames {
		method := &expr.MethodExpr{
			Name: methodName, Service: svc,
			Payload: &expr.AttributeExpr{Type: expr.Empty}, Result: &expr.AttributeExpr{Type: expr.String},
			StreamingPayload: &expr.AttributeExpr{Type: expr.Empty}, StreamingResult: &expr.AttributeExpr{Type: expr.Empty},
		}
		svc.Methods = append(svc.Methods, method)
		methods[methodName] = method
	}
	return svc, methods
}

func testResourceQueryPayload() *expr.AttributeExpr {
	baseQuery := &expr.UserTypeExpr{TypeName: "ResourceQueryBase", AttributeExpr: &expr.AttributeExpr{
		Type:       &expr.Object{{Name: "tenant", Attribute: &expr.AttributeExpr{Type: expr.String}}},
		Validation: &expr.ValidationExpr{Required: []string{"tenant"}},
	}}
	return &expr.AttributeExpr{
		Type: &expr.Object{
			{Name: "cursor", Attribute: &expr.AttributeExpr{Type: expr.String}},
			{Name: "offset", Attribute: &expr.AttributeExpr{Type: expr.Int}},
			{Name: "limit", Attribute: &expr.AttributeExpr{Type: expr.UInt, DefaultValue: 25}},
			{Name: "enabled", Attribute: &expr.AttributeExpr{Type: expr.Boolean}},
			{Name: "ratio", Attribute: &expr.AttributeExpr{Type: expr.Float64}},
			{Name: "tags", Attribute: &expr.AttributeExpr{Type: &expr.Array{ElemType: &expr.AttributeExpr{Type: expr.String}}}},
		},
		Bases: []expr.DataType{baseQuery}, Validation: &expr.ValidationExpr{Required: []string{"cursor"}},
	}
}

func testRootExpr(services []*expr.ServiceExpr, jsonrpcServices []*expr.HTTPServiceExpr) *expr.RootExpr {
	httpServices := make([]*expr.HTTPServiceExpr, 0, len(services))
	servers := make([]*expr.ServerExpr, 0, len(services))
	for _, svc := range services {
		httpServices = append(httpServices, httpService(svc))
		servers = append(servers, &expr.ServerExpr{Name: svc.Name + "-server", Services: []string{svc.Name}})
	}
	return &expr.RootExpr{
		Services: services,
		API: &expr.APIExpr{
			ExampleGenerator: &expr.ExampleGenerator{Randomizer: expr.NewFakerRandomizer("mcp-contract-test")},
			HTTP:             &expr.HTTPExpr{Services: httpServices},
			JSONRPC:          &expr.JSONRPCExpr{HTTPExpr: expr.HTTPExpr{Services: jsonrpcServices}},
			GRPC:             &expr.GRPCExpr{}, Servers: servers,
		},
	}
}

func httpService(svc *expr.ServiceExpr) *expr.HTTPServiceExpr {
	return &expr.HTTPServiceExpr{ServiceExpr: svc}
}

func jsonrpcService(svc *expr.ServiceExpr, path string) *expr.HTTPServiceExpr {
	return jsonrpcServiceWithMethod(svc, path, http.MethodPost)
}

func jsonrpcServiceWithMethod(svc *expr.ServiceExpr, path string, method string) *expr.HTTPServiceExpr {
	return &expr.HTTPServiceExpr{ServiceExpr: svc, JSONRPCRoute: &expr.RouteExpr{Method: method, Path: path}}
}

func findGeneratedFile(t *testing.T, files []*gcodegen.File, path string) *gcodegen.File {
	t.Helper()
	want := filepath.ToSlash(path)
	for _, file := range files {
		if filepath.ToSlash(file.Path) == want {
			return file
		}
	}
	require.Failf(t, "generated file not found", "missing %s", want)
	return nil
}

func renderGeneratedFile(t *testing.T, file *gcodegen.File) string {
	t.Helper()

	var output bytes.Buffer
	for _, section := range file.AllSections() {
		switch sec := section.(type) {
		case *gcodegen.SectionTemplate:
			tmpl := template.New(sec.Name).Funcs(template.FuncMap{
				"comment":     gcodegen.Comment,
				"commandLine": func() string { return "" },
			})
			if sec.FuncMap != nil {
				tmpl = tmpl.Funcs(sec.FuncMap)
			}
			parsed, err := tmpl.Parse(sec.Source)
			require.NoError(t, err)
			var rendered bytes.Buffer
			require.NoError(t, parsed.Execute(&rendered, sec.Data))
			output.Write(rendered.Bytes())
		default:
			require.NoError(t, section.Write(&output), "render %s", section.SectionName())
		}
	}

	require.NotEmpty(t, strings.TrimSpace(output.String()), filepath.ToSlash(file.Path))
	return output.String()
}
