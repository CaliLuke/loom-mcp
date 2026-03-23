package codegen

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/CaliLuke/loom-mcp/codegen/shared"
	mcpexpr "github.com/CaliLuke/loom-mcp/expr/mcp"
	"github.com/CaliLuke/loom-mcp/internal/upstreampaths"
	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/codegen/service"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
	httpcodegen "github.com/CaliLuke/loom/http/codegen"
	jsonrpccodegen "github.com/CaliLuke/loom/jsonrpc/codegen"
)

const headerSection = "source-header"
const exampleMCPStubSection = "example-mcp-stub"

// Generate orchestrates MCP code generation for services that declare MCP
// configuration in the DSL. It composes Goa service and JSON-RPC generators
// and adds adapter/client helpers.
func Generate(genpkg string, roots []eval.Root, files []*codegen.File) ([]*codegen.File, error) {
	// Process MCP services from source snapshot and preserve deterministic order.
	source := collectSourceSnapshot(roots)

	// Process MCP services from original services
	for _, svc := range source.services {
		if !mcpexpr.Root.HasMCP(svc) {
			continue
		}

		// Generate MCP service with MCP endpoints
		mcp := mcpexpr.Root.GetMCP(svc)
		if err := validatePureMCPService(svc, mcp, source); err != nil {
			return nil, err
		}

		// Build MCP service expression
		exprBuilder := newMCPExprBuilder(svc, mcp, source)
		mcpService := exprBuilder.BuildServiceExpr()

		// Create temporary root for MCP generation
		mcpRoot := exprBuilder.BuildRootExpr(mcpService)

		// Prepare, validate, and finalize MCP expressions
		if err := exprBuilder.PrepareAndValidate(mcpRoot); err != nil {
			return nil, fmt.Errorf("MCP expression validation failed: %w", err)
		}

		// Build mapping and adapter data early so we can customize generated clients
		mapping := exprBuilder.BuildServiceMapping()
		adapterGen := newAdapterGenerator(genpkg, svc, mcp, mapping)
		adapterData, err := adapterGen.buildAdapterData()
		if err != nil {
			return nil, fmt.Errorf("build adapter data for %s: %w", svc.Name, err)
		}
		if reg := registerFile(adapterData); reg != nil {
			files = append(files, reg)
		}
		if caller := clientCallerFile(adapterData, codegen.SnakeCase(svc.Name)); caller != nil {
			files = append(files, caller)
		}

		// Generate MCP service code using Goa's standard generators (with retry hooks)
		mcpFiles := generateMCPServiceCode(genpkg, mcpRoot, mcpService)
		files = append(files, mcpFiles...)

		// Generate MCP transport that wraps the original service
		files = append(files, generateMCPTransport(genpkg, svc, adapterData)...)

		// Generate MCP client adapter that wraps the MCP JSON-RPC client
		clientAdapterFiles := generateMCPClientAdapter(genpkg, svc, adapterData)
		files = append(files, clientAdapterFiles...)
	}

	return files, nil
}

// generateMCPServiceCode generates the MCP service layer and JSON-RPC transport
// using Goa's built-in generators.
func generateMCPServiceCode(genpkg string, root *expr.RootExpr, mcpService *expr.ServiceExpr) []*codegen.File {
	files := make([]*codegen.File, 0, 16)

	// Create services data from temporary MCP root
	servicesData := service.NewServicesData(root)

	// Generate MCP service layer only (no HTTP transports for original service)
	userTypePkgs := make(map[string][]string)
	serviceFiles := service.Files(genpkg, mcpService, servicesData, userTypePkgs)
	for _, f := range serviceFiles {
		if strings.HasSuffix(filepath.ToSlash(f.Path), "/service.go") {
			service.AddServiceDataMetaTypeImports(f.HeaderTemplate(), mcpService, servicesData.Get(mcpService.Name))
		}
	}
	files = append(files, serviceFiles...)
	files = append(files, service.EndpointFile(genpkg, mcpService, servicesData))
	files = append(files, service.ClientFile(genpkg, mcpService, servicesData))

	// Generate JSON-RPC transport for MCP service only
	httpServices := httpcodegen.NewServicesData(servicesData, &root.API.JSONRPC.HTTPExpr)
	httpServices.Root = root

	// Generate both base and SSE server files.
	files = append(files, jsonrpccodegen.ServerFiles(genpkg, httpServices)...)
	files = append(files, jsonrpccodegen.SSEServerFiles(genpkg, httpServices)...)
	files = append(files, jsonrpccodegen.ServerTypeFiles(genpkg, httpServices)...)
	files = append(files, jsonrpccodegen.PathFiles(httpServices)...)
	// Add client-side JSON-RPC for MCP service so adapters can depend on it
	files = append(files, jsonrpccodegen.ClientTypeFiles(genpkg, httpServices)...)
	files = append(files, jsonrpccodegen.ClientFiles(genpkg, httpServices)...)

	applyMCPPolicyHeadersToJSONRPCMount(files)
	return files
}

// applyMCPPolicyHeadersToJSONRPCMount replaces the JSON-RPC server mount section
// with a loom-mcp-owned template that propagates MCP policy headers into the
// request context.
//
// This avoids any string-based patching while ensuring header-driven allow/deny
// policy can be enforced by MCP adapters without requiring example/server wiring
// changes.
func applyMCPPolicyHeadersToJSONRPCMount(files []*codegen.File) {
	for _, f := range files {
		if f == nil {
			continue
		}
		if filepath.Base(filepath.Dir(filepath.ToSlash(f.Path))) != "server" || filepath.Base(f.Path) != "server.go" {
			continue
		}
		sections := f.AllSections()
		if len(sections) > 0 {
			updated := make([]codegen.Section, 0, len(sections))
			for _, sec := range sections {
				updated = append(updated, replaceJSONRPCServerSection(sec))
			}
			f.SetSections(updated)
		}
		if header := f.HeaderTemplate(); header != nil {
			codegen.AddImport(header, &codegen.ImportSpec{Path: "encoding/json"})
			codegen.AddImport(header, &codegen.ImportSpec{Path: "github.com/CaliLuke/loom-mcp/runtime/mcp", Name: "mcpruntime"})
		}
	}
}

func replaceJSONRPCServerSection(section codegen.Section) codegen.Section {
	switch sec := section.(type) {
	case *codegen.SectionTemplate:
		if sec == nil {
			return nil
		}
		if source, ok := mcpJSONRPCServerSectionSource(sec.Name); ok {
			clone := *sec
			clone.Source = source
			return &clone
		}
		return sec
	case *codegen.RawSection:
		if sec == nil {
			return nil
		}
		if sec.Name == "jsonrpc-server-mount" {
			return codegen.NewRawSection(sec.Name, rewriteJSONRPCServerMountSource(sec.Source))
		}
		return sec
	default:
		return section
	}
}

func mcpJSONRPCServerSectionSource(name string) (string, bool) {
	switch name {
	case "jsonrpc-server-struct":
		return mcpTemplates.Read("jsonrpc_server_struct"), true
	case "jsonrpc-server-init":
		return mcpTemplates.Read("jsonrpc_server_init"), true
	case "jsonrpc-server-handler":
		return mcpTemplates.Read("jsonrpc_server_handler"), true
	case "jsonrpc-mixed-server-handler":
		return mcpTemplates.Read("jsonrpc_mixed_server_handler"), true
	case "jsonrpc-server-mount":
		return mcpTemplates.Read("jsonrpc_server_mount"), true
	default:
		return "", false
	}
}

func rewriteJSONRPCServerMountSource(source string) string {
	if source == "" {
		return source
	}

	updated := source
	updated = strings.ReplaceAll(updated, ", h.ServeHTTP)\n", ", withMCPPolicyHeaders(h.ServeHTTP))\n")
	updated = strings.ReplaceAll(updated, ", h.handleSSE)\n", ", withMCPPolicyHeaders(h.handleSSE))\n")

	if strings.Contains(updated, "Mixed transports:") {
		updated = addMixedTransportSessionRoutes(updated)
	}
	if strings.Contains(updated, "func withMCPPolicyHeaders(") {
		return updated
	}
	return strings.TrimRight(updated, "\n") + jsonrpcServerMountHelperSource
}

func addMixedTransportSessionRoutes(source string) string {
	lines := strings.Split(source, "\n")
	insertAt := -1
	paths := make([]string, 0, 1)
	seenPaths := make(map[string]struct{})
	seenMethods := make(map[string]map[string]struct{})
	inMount := false

	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func ") && strings.Contains(trimmed, "(mux goahttp.Muxer, h *") {
			inMount = true
			continue
		}
		if !inMount {
			continue
		}
		if trimmed == "}" {
			insertAt = idx
			break
		}
		method, path, ok := parseMuxHandleCall(trimmed)
		if !ok {
			continue
		}
		if _, ok := seenPaths[path]; !ok {
			seenPaths[path] = struct{}{}
			paths = append(paths, path)
		}
		if seenMethods[path] == nil {
			seenMethods[path] = make(map[string]struct{})
		}
		seenMethods[path][method] = struct{}{}
	}
	if insertAt == -1 {
		return source
	}

	extra := make([]string, 0, len(paths)*2)
	for _, path := range paths {
		for _, method := range []string{"GET", "DELETE"} {
			if _, ok := seenMethods[path][method]; ok {
				continue
			}
			extra = append(extra, fmt.Sprintf("\tmux.Handle(%q, %q, withMCPPolicyHeaders(h.ServeHTTP))", method, path))
		}
	}
	if len(extra) == 0 {
		return source
	}

	updated := make([]string, 0, len(lines)+len(extra))
	updated = append(updated, lines[:insertAt]...)
	updated = append(updated, extra...)
	updated = append(updated, lines[insertAt:]...)
	return strings.Join(updated, "\n")
}

func parseMuxHandleCall(line string) (method, path string, ok bool) {
	if !strings.HasPrefix(line, "mux.Handle(") {
		return "", "", false
	}
	rest := strings.TrimPrefix(line, "mux.Handle(")
	parts := strings.SplitN(rest, ",", 3)
	if len(parts) != 3 {
		return "", "", false
	}
	method = strings.Trim(parts[0], " \t\"")
	path = strings.Trim(parts[1], " \t\"")
	if method == "" || path == "" {
		return "", "", false
	}
	return method, path, true
}

const jsonrpcServerMountHelperSource = `

// withMCPPolicyHeaders propagates MCP policy header values into the request context.
//
// The MCP adapter enforces resource allow/deny policies based on context values:
//   - "mcp_allow_names" (CSV list of resource names)
//   - "mcp_deny_names"  (CSV list of resource names)
//
// This helper maps those values from the corresponding HTTP headers:
//   - x-mcp-allow-names
//   - x-mcp-deny-names
//
// It is installed by the JSON-RPC Mount functions so consumers do not need
// to patch example servers or wire middleware manually.
func withMCPPolicyHeaders(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if allow := r.Header.Get("x-mcp-allow-names"); allow != "" {
			ctx = context.WithValue(ctx, "mcp_allow_names", allow)
		}
		if deny := r.Header.Get("x-mcp-deny-names"); deny != "" {
			ctx = context.WithValue(ctx, "mcp_deny_names", deny)
		}
		if sessionID := r.Header.Get(mcpruntime.HeaderKeySessionID); sessionID != "" {
			ctx = mcpruntime.WithSessionID(ctx, sessionID)
		}
		ctx = mcpruntime.WithResponseWriter(ctx, w)
		next(w, r.WithContext(ctx))
	}
}
`

// generateMCPTransport generates adapter and prompt provider files that adapt
// MCP protocol methods to the original service implementation.
func generateMCPTransport(genpkg string, svc *expr.ServiceExpr, data *AdapterData) []*codegen.File {
	var files []*codegen.File
	svcName := codegen.SnakeCase(svc.Name)

	// Generate server adapter in gen/mcp_<service>/adapter_server.go (same package as MCP service)
	adapterPath := filepath.Join(codegen.Gendir, "mcp_"+svcName, "adapter_server.go")
	pkgName := data.MCPPackage

	adapterImports := []*codegen.ImportSpec{
		{Path: "bytes"},
		{Path: "context"},
		{Path: "encoding/json"},
		{Path: "errors"},
		{Path: "fmt"},
		{Path: "io"},
		{Path: "net/http"},
		{Path: "net/url"},
		{Path: "path"},
		{Path: "strconv"},
		{Path: "strings"},
		{Path: "sync"},
		{Path: "time"},
		{Path: "github.com/modelcontextprotocol/go-sdk/auth", Name: "mcpauth"},
		{Path: "go.opentelemetry.io/otel"},
		{Path: "go.opentelemetry.io/otel/attribute"},
		{Path: "go.opentelemetry.io/otel/codes"},
		{Path: "go.opentelemetry.io/otel/metric"},
		{Path: "go.opentelemetry.io/otel/trace"},
		{Path: genpkg + "/" + svcName, Name: svcName},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/mcp", Name: "mcpruntime"},
		{Path: upstreampaths.LoomMCPHTTPImportPath, Name: "goahttp"},
		{Path: upstreampaths.LoomPkgImportPath, Name: "goa"},
	}
	// Include external user type imports referenced by method payloads/results.
	existing := make(map[string]struct{}, len(adapterImports))
	for _, im := range adapterImports {
		if im != nil && im.Path != "" {
			existing[im.Path] = struct{}{}
		}
	}
	extra := make(map[string]*codegen.ImportSpec)
	for _, m := range svc.Methods {
		if m.Payload != nil {
			for _, im := range shared.GatherAttributeImports(genpkg, m.Payload) {
				if im != nil && im.Path != "" {
					extra[im.Path] = im
				}
			}
		}
		if m.Result != nil {
			for _, im := range shared.GatherAttributeImports(genpkg, m.Result) {
				if im != nil && im.Path != "" {
					extra[im.Path] = im
				}
			}
		}
	}
	if len(extra) > 0 {
		// Deterministic order
		paths := make([]string, 0, len(extra))
		for p := range extra {
			if _, ok := existing[p]; ok {
				continue
			}
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			adapterImports = append(adapterImports, extra[p])
		}
	}
	files = append(files, &codegen.File{
		Path: adapterPath,
		SectionTemplates: []*codegen.SectionTemplate{
			codegen.Header(fmt.Sprintf("MCP server adapter for %s service", svc.Name), pkgName, adapterImports),
			{
				Name:   "mcp-adapter-core",
				Source: mcpTemplates.Read("adapter_core"),
				Data:   data,
				FuncMap: map[string]any{
					"goify":   func(s string) string { return codegen.Goify(s, true) },
					"comment": codegen.Comment,
					"quote":   func(s string) string { return fmt.Sprintf("%q", s) },
				},
			},
			{
				Name:   "mcp-adapter-broadcast",
				Source: mcpTemplates.Read("adapter_broadcast"),
				Data:   data,
				FuncMap: map[string]any{
					"goify":   func(s string) string { return codegen.Goify(s, true) },
					"comment": codegen.Comment,
					"quote":   func(s string) string { return fmt.Sprintf("%q", s) },
				},
			},
			{
				Name:   "mcp-adapter-tools",
				Source: mcpTemplates.Read("adapter_tools"),
				Data:   data,
				FuncMap: map[string]any{
					"goify":   func(s string) string { return codegen.Goify(s, true) },
					"comment": codegen.Comment,
					"quote":   func(s string) string { return fmt.Sprintf("%q", s) },
				},
			},
			{
				Name:   "mcp-adapter-resources",
				Source: mcpTemplates.Read("adapter_resources"),
				Data:   data,
				FuncMap: map[string]any{
					"goify":   func(s string) string { return codegen.Goify(s, true) },
					"comment": codegen.Comment,
					"quote":   func(s string) string { return fmt.Sprintf("%q", s) },
				},
			},
			{
				Name:   "mcp-adapter-prompts",
				Source: mcpTemplates.Read("adapter_prompts"),
				Data:   data,
				FuncMap: map[string]any{
					"goify":   func(s string) string { return codegen.Goify(s, true) },
					"comment": codegen.Comment,
					"quote":   func(s string) string { return fmt.Sprintf("%q", s) },
				},
			},
			{
				Name:   "mcp-adapter-notifications",
				Source: mcpTemplates.Read("adapter_notifications"),
				Data:   data,
				FuncMap: map[string]any{
					"goify":   func(s string) string { return codegen.Goify(s, true) },
					"comment": codegen.Comment,
					"quote":   func(s string) string { return fmt.Sprintf("%q", s) },
				},
			},
			{
				Name:   "mcp-adapter-subscriptions",
				Source: mcpTemplates.Read("adapter_subscriptions"),
				Data:   data,
				FuncMap: map[string]any{
					"goify":   func(s string) string { return codegen.Goify(s, true) },
					"comment": codegen.Comment,
					"quote":   func(s string) string { return fmt.Sprintf("%q", s) },
				},
			},
		},
	})

	// Generate protocol version constant in MCP package
	versionPath := filepath.Join(codegen.Gendir, "mcp_"+svcName, "protocol_version.go")
	versionImports := []*codegen.ImportSpec{}
	pv := data.ProtocolVersion
	if pv == "" {
		// Default to integration test expected version when none provided via DSL
		pv = "2025-06-18"
	}
	files = append(files, &codegen.File{
		Path: versionPath,
		SectionTemplates: []*codegen.SectionTemplate{
			codegen.Header("MCP protocol version", pkgName, versionImports),
			{
				Name:   "mcp-protocol-version",
				Source: fmt.Sprintf("const DefaultProtocolVersion = %q\n", pv),
			},
		},
	})

	sdkServerPath := filepath.Join(codegen.Gendir, "mcp_"+svcName, "sdk_server.go")
	sdkServerImports := []*codegen.ImportSpec{
		{Path: "context"},
		{Path: "encoding/base64"},
		{Path: "encoding/json"},
		{Path: "fmt"},
		{Path: "net/http"},
		{Path: "net/url"},
		{Path: "strings"},
		{Path: "time"},
		{Path: genpkg + "/" + svcName, Name: svcName},
		{Path: "github.com/modelcontextprotocol/go-sdk/auth", Name: "mcpauth"},
		{Path: "github.com/modelcontextprotocol/go-sdk/mcp", Name: "mcpsdk"},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/mcp", Name: "mcpruntime"},
	}
	files = append(files, &codegen.File{
		Path: sdkServerPath,
		SectionTemplates: []*codegen.SectionTemplate{
			codegen.Header(fmt.Sprintf("SDK-backed MCP server for %s service", svc.Name), pkgName, sdkServerImports),
			{
				Name:   "mcp-sdk-server",
				Source: mcpTemplates.Read("sdk_server"),
				Data:   data,
				FuncMap: map[string]any{
					"goify": func(s string) string { return codegen.Goify(s, true) },
					"quote": func(s string) string { return fmt.Sprintf("%q", s) },
				},
			},
		},
	})

	// If prompts are present, generate prompt_provider in a separate file (same package)
	if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
		providerPath := filepath.Join(codegen.Gendir, "mcp_"+svcName, "prompt_provider.go")
		providerImports := []*codegen.ImportSpec{
			{Path: "context"},
			{Path: "encoding/json"},
			{Path: genpkg + "/" + svcName, Name: svcName},
		}
		files = append(files, &codegen.File{
			Path: providerPath,
			SectionTemplates: []*codegen.SectionTemplate{
				codegen.Header(fmt.Sprintf("MCP prompt provider for %s service", svc.Name), pkgName, providerImports),
				{
					Name:   "mcp-prompt-provider",
					Source: mcpTemplates.Read("prompt_provider"),
					Data:   data,
					FuncMap: map[string]any{
						"goify": func(s string) string { return codegen.Goify(s, true) },
					},
				},
			},
		})
	}

	return files
}

// generateMCPClientAdapter generates a client adapter that exposes the original
// service endpoints while calling MCP JSON-RPC methods under the hood.
func generateMCPClientAdapter(genpkg string, svc *expr.ServiceExpr, data *AdapterData) []*codegen.File {
	files := make([]*codegen.File, 0, 1)

	svcName := codegen.SnakeCase(svc.Name)
	// Match the package alias used elsewhere (strip underscores)
	mcpPkgAlias := codegen.Goify("mcp_"+svcName, false)
	svcJSONRPCCAlias := svcName + "jsonrpcc"
	mcpJSONRPCCAlias := mcpPkgAlias + "jsonrpcc"

	// Extend data passed to template with aliases needed by imports
	type methodInfo struct {
		Name     string
		IsMapped bool // Whether this method is mapped to an MCP construct
	}

	type clientAdapterTemplateData struct {
		*AdapterData
		ServiceGoName    string
		ServicePkg       string
		MCPPkgAlias      string
		SvcJSONRPCCAlias string
		MCPJSONRPCCAlias string
		AllMethods       []methodInfo // All service methods with mapping info
	}

	// Build set of mapped methods
	mapped := make(map[string]struct{})
	for _, t := range data.Tools {
		mapped[t.OriginalMethodName] = struct{}{}
	}
	for _, r := range data.Resources {
		mapped[r.OriginalMethodName] = struct{}{}
	}
	for _, dp := range data.DynamicPrompts {
		mapped[dp.OriginalMethodName] = struct{}{}
	}
	for _, n := range data.Notifications {
		mapped[n.OriginalMethodName] = struct{}{}
	}

	// Collect all service method names and check if they're mapped to MCP constructs
	allMethods := make([]methodInfo, len(svc.Methods))
	for i, m := range svc.Methods {
		methodName := codegen.Goify(m.Name, true)
		_, ok := mapped[methodName]
		allMethods[i] = methodInfo{
			Name:     methodName,
			IsMapped: ok,
		}
	}

	tdata := &clientAdapterTemplateData{
		AdapterData:      data,
		ServiceGoName:    codegen.Goify(svc.Name, true),
		ServicePkg:       svcName,
		MCPPkgAlias:      mcpPkgAlias,
		SvcJSONRPCCAlias: svcJSONRPCCAlias,
		MCPJSONRPCCAlias: mcpJSONRPCCAlias,
		AllMethods:       allMethods,
	}

	imports := []*codegen.ImportSpec{
		{Path: "bytes"},
		{Path: "context"},
		{Path: "encoding/json", Name: "stdjson"},
		{Path: "fmt"},
		{Path: "io"},
		{Path: "net/url"},
		{Path: "net/http"},
		{Path: "sync"},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/mcp", Name: "mcpruntime"},
		{Path: upstreampaths.LoomMCPHTTPImportPath, Name: "goahttp"},
		{Path: upstreampaths.LoomMCPJSONRPCImportPath, Name: "jsonrpc"},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/mcp/retry", Name: "retry"},
		{Path: genpkg + "/" + svcName, Name: svcName},
		{Path: genpkg + "/jsonrpc/" + svcName + "/client", Name: svcJSONRPCCAlias},
		// Import the MCP service package for types since we're now in a subpackage
		{Path: genpkg + "/mcp_" + svcName, Name: mcpPkgAlias},
		{Path: genpkg + "/jsonrpc/mcp_" + svcName + "/client", Name: mcpJSONRPCCAlias},
	}
	if data.NeedsQueryFormatting {
		imports = append(imports, &codegen.ImportSpec{Path: "strconv"})
	}

	// Put client adapter in a separate subpackage to avoid import cycle
	adapterPkgName := mcpPkgAlias + "adapter"
	files = append(files, &codegen.File{
		Path: filepath.Join(codegen.Gendir, "mcp_"+svcName, "adapter", "client", "adapter.go"),
		SectionTemplates: []*codegen.SectionTemplate{
			codegen.Header("MCP client adapter exposing original service endpoints", adapterPkgName, imports),
			{
				Name:   "mcp-client-adapter",
				Source: mcpTemplates.Read("mcp_client_wrapper"),
				Data:   tdata,
				FuncMap: map[string]any{
					"comment": codegen.Comment,
					"goify": func(s string) string {
						return codegen.Goify(s, true)
					},
					"queryValueExpr": queryValueExpr,
				},
			},
		},
	})

	return files
}

// queryValueExpr returns the direct Go expression that formats one statically
// known resource query value into the string form expected by url.Values.
func queryValueExpr(formatKind, valueExpr string) string {
	switch formatKind {
	case resourceQueryFormatString:
		return valueExpr
	case resourceQueryFormatBool:
		return fmt.Sprintf("strconv.FormatBool(%s)", valueExpr)
	case resourceQueryFormatInt:
		return fmt.Sprintf("strconv.FormatInt(int64(%s), 10)", valueExpr)
	case resourceQueryFormatUint:
		return fmt.Sprintf("strconv.FormatUint(uint64(%s), 10)", valueExpr)
	case resourceQueryFormatFloat32:
		return fmt.Sprintf("strconv.FormatFloat(float64(%s), 'g', -1, 32)", valueExpr)
	case resourceQueryFormatFloat64:
		return fmt.Sprintf("strconv.FormatFloat(%s, 'g', -1, 64)", valueExpr)
	default:
		panic(fmt.Sprintf("unsupported resource query format kind %q", formatKind))
	}
}
