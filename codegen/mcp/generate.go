package codegen

import (
	"bytes"
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
const jsonrpcServerMountSectionName = "jsonrpc-server-mount"

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
		rewriteJSONRPCServerFile(f)
		if header := f.HeaderTemplate(); header != nil {
			codegen.AddImport(header, &codegen.ImportSpec{Path: "encoding/json"})
			codegen.AddImport(header, &codegen.ImportSpec{Path: "github.com/CaliLuke/loom-mcp/runtime/mcp", Name: "mcpruntime"})
		}
	}
}

func rewriteJSONRPCServerFile(file *codegen.File) {
	sections := file.AllSections()
	if len(sections) == 0 {
		return
	}
	updated := make([]codegen.Section, 0, len(sections))
	for _, section := range sections {
		updated = append(updated, rewriteJSONRPCServerSection(section))
	}
	file.SetSections(updated)
}

func rewriteJSONRPCServerSection(section codegen.Section) codegen.Section {
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
		if rewritten, ok := rewriteJSONRPCSectionByRenderedSource(sec); ok {
			return rewritten
		}
		return sec
	case *codegen.RawSection, *codegen.JenniferSection:
		if rewritten, ok := rewriteJSONRPCSectionByRenderedSource(sec); ok {
			return rewritten
		}
	default:
		if rewritten, ok := rewriteJSONRPCSectionByRenderedSource(section); ok {
			return rewritten
		}
	}
	return section
}

func rewriteJSONRPCSectionByRenderedSource(section codegen.Section) (codegen.Section, bool) {
	if section == nil {
		return nil, false
	}
	source, ok := renderedSectionSource(section)
	if !ok {
		return nil, false
	}
	if section.SectionName() != jsonrpcServerMountSectionName && !isJSONRPCMountSource(source) {
		return nil, false
	}
	return &codegen.RawSection{
		Name:   section.SectionName(),
		Source: rewriteJSONRPCServerMountSource(source),
	}, true
}

func renderedSectionSource(section codegen.Section) (string, bool) {
	var buf bytes.Buffer
	if err := section.Write(&buf); err != nil {
		return "", false
	}
	return buf.String(), true
}

func isJSONRPCMountSource(source string) bool {
	return strings.Contains(source, "configures the mux to serve the JSON-RPC") &&
		strings.Contains(source, "mux.Handle(") &&
		(strings.Contains(source, "h.ServeHTTP") || strings.Contains(source, "h.handleSSE"))
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

	pkgName := data.MCPPackage
	files = append(files, buildMCPAdapterFile(genpkg, svc, data, svcName))
	files = append(files, buildMCPProtocolVersionFile(pkgName, svcName, data.ProtocolVersion))
	files = append(files, buildMCPSDKServerFile(genpkg, svc, data, svcName, pkgName))
	if provider := buildMCPPromptProviderFile(genpkg, svc, data, svcName, pkgName); provider != nil {
		files = append(files, provider)
	}
	return files
}

// generateMCPClientAdapter generates a client adapter that exposes the original
// service endpoints while calling MCP JSON-RPC methods under the hood.
func generateMCPClientAdapter(genpkg string, svc *expr.ServiceExpr, data *AdapterData) []*codegen.File {
	if file := clientAdapterFile(genpkg, svc, data); file != nil {
		return []*codegen.File{file}
	}
	return nil
}

func buildMCPAdapterFile(genpkg string, svc *expr.ServiceExpr, data *AdapterData, svcName string) *codegen.File {
	adapterPath := filepath.Join(codegen.Gendir, "mcp_"+svcName, "adapter_server.go")
	return &codegen.File{
		Path: adapterPath,
		Sections: []codegen.Section{
			codegen.Header(fmt.Sprintf("MCP server adapter for %s service", svc.Name), data.MCPPackage, adapterImports(genpkg, svc, svcName)),
			templateSection("mcp-adapter-core", "adapter_core", data),
			adapterBroadcastSection(),
			templateSection("mcp-adapter-tools", "adapter_tools", data),
			adapterResourcesSection(data),
			adapterPromptsSection(data),
			adapterNotificationsSection(),
			adapterSubscriptionsSection(data),
		},
	}
}

func adapterImports(genpkg string, svc *expr.ServiceExpr, svcName string) []*codegen.ImportSpec {
	imports := make([]*codegen.ImportSpec, 0, 24)
	imports = append(imports, []*codegen.ImportSpec{
		{Path: "bytes"},
		{Path: "context"},
		{Path: "encoding/json"},
		{Path: "errors"},
		{Path: "fmt"},
		{Path: "io"},
		{Path: "net/http"},
		{Path: "net/url"},
		{Path: "path"},
		{Path: "sort"},
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
	}...)
	return append(imports, adapterAttributeImports(genpkg, svc, imports)...)
}

func adapterAttributeImports(genpkg string, svc *expr.ServiceExpr, imports []*codegen.ImportSpec) []*codegen.ImportSpec {
	existing := make(map[string]struct{}, len(imports))
	for _, im := range imports {
		if im != nil && im.Path != "" {
			existing[im.Path] = struct{}{}
		}
	}
	extra := make(map[string]*codegen.ImportSpec)
	for _, m := range svc.Methods {
		addAttributeImports(extra, genpkg, m.Payload)
		addAttributeImports(extra, genpkg, m.Result)
	}
	paths := make([]string, 0, len(extra))
	for p := range extra {
		if _, ok := existing[p]; ok {
			continue
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	result := make([]*codegen.ImportSpec, 0, len(paths))
	for _, p := range paths {
		result = append(result, extra[p])
	}
	return result
}

func addAttributeImports(target map[string]*codegen.ImportSpec, genpkg string, attr *expr.AttributeExpr) {
	if attr == nil {
		return
	}
	for _, im := range shared.GatherAttributeImports(genpkg, attr) {
		if im != nil && im.Path != "" {
			target[im.Path] = im
		}
	}
}

func buildMCPProtocolVersionFile(pkgName, svcName, protocolVersion string) *codegen.File {
	pv := protocolVersion
	if pv == "" {
		pv = "2025-06-18"
	}
	return &codegen.File{
		Path: filepath.Join(codegen.Gendir, "mcp_"+svcName, "protocol_version.go"),
		SectionTemplates: []*codegen.SectionTemplate{
			codegen.Header("MCP protocol version", pkgName, nil),
			{Name: "mcp-protocol-version", Source: fmt.Sprintf("const DefaultProtocolVersion = %q\n", pv)},
		},
	}
}

func buildMCPPromptProviderFile(genpkg string, svc *expr.ServiceExpr, data *AdapterData, svcName, pkgName string) *codegen.File {
	if len(data.StaticPrompts) == 0 && len(data.DynamicPrompts) == 0 {
		return nil
	}
	return &codegen.File{
		Path: filepath.Join(codegen.Gendir, "mcp_"+svcName, "prompt_provider.go"),
		Sections: []codegen.Section{
			codegen.Header(fmt.Sprintf("MCP prompt provider for %s service", svc.Name), pkgName, []*codegen.ImportSpec{
				{Path: "context"},
				{Path: "encoding/json"},
				{Path: genpkg + "/" + svcName, Name: svcName},
			}),
			promptProviderSection(data),
		},
	}
}

func templateSection(name, templateName string, data *AdapterData) *codegen.SectionTemplate {
	return &codegen.SectionTemplate{
		Name:   name,
		Source: mcpTemplates.Read(templateName),
		Data:   data,
		FuncMap: map[string]any{
			"goify":   func(s string) string { return codegen.Goify(s, true) },
			"comment": codegen.Comment,
			"quote":   func(s string) string { return fmt.Sprintf("%q", s) },
		},
	}
}
