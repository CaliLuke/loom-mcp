package codegen

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	agentcodegen "github.com/CaliLuke/loom-mcp/codegen/agent"
	"github.com/CaliLuke/loom-mcp/codegen/shared"
	agentsexpr "github.com/CaliLuke/loom-mcp/expr/agent"
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
		projected, err := ProjectedToolInventory(genpkg, roots, svc.Name, mcp.Name)
		if err != nil {
			return nil, fmt.Errorf("build projected tool inventory for %s.%s: %w", svc.Name, mcp.Name, err)
		}
		adapterGen := newAdapterGenerator(genpkg, svc, mcp, mapping, projected)
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
		mcpFiles, err := generateMCPServiceCode(genpkg, mcpRoot, mcpService, adapterData.ProtocolVersion)
		if err != nil {
			return nil, err
		}
		files = append(files, mcpFiles...)

		// Generate MCP transport that wraps the original service
		files = append(files, generateMCPTransport(genpkg, svc, adapterData)...)

		// Generate MCP client adapter that wraps the MCP JSON-RPC client
		clientAdapterFiles := generateMCPClientAdapter(genpkg, svc, adapterData)
		files = append(files, clientAdapterFiles...)
	}

	return files, nil
}

// ProjectedToolInventory returns method-backed toolset tools placed into a generated MCP server.
func ProjectedToolInventory(genpkg string, roots []eval.Root, serviceName string, mcpServer string) ([]*ProjectedToolAdapter, error) {
	if !hasAgentRoot(roots) {
		return nil, nil
	}
	tools, err := agentcodegen.ProjectedMCPTools(genpkg, roots, serviceName, mcpServer)
	if err != nil {
		return nil, err
	}
	out := make([]*ProjectedToolAdapter, 0, len(tools))
	for _, tool := range tools {
		inputSchema := ""
		if tool.Args != nil {
			schema, err := shared.ToJSONSchema(tool.Args)
			if err != nil {
				return nil, fmt.Errorf("build projected input schema for %q: %w", tool.QualifiedName, err)
			}
			inputSchema = schema
		}
		outputSchema := ""
		if tool.Return != nil {
			schema, err := shared.ToJSONSchema(tool.Return)
			if err != nil {
				return nil, fmt.Errorf("build projected output schema for %q: %w", tool.QualifiedName, err)
			}
			outputSchema = schema
		}
		out = append(out, &ProjectedToolAdapter{
			SourceToolset:       tool.Toolset.Name,
			SourceTool:          tool.Name,
			Description:         tool.Description,
			Title:               tool.Title,
			PlacementService:    tool.MCPPlacementService,
			PlacementMCPServer:  tool.MCPPlacementServer,
			SpecsPackageName:    tool.Toolset.SpecsPackageName,
			SpecsImportPath:     tool.Toolset.SpecsImportPath,
			SpecName:            "Spec" + tool.ConstName,
			BoundService:        tool.Toolset.SourceServiceName,
			BoundMethod:         tool.MethodGoName,
			MethodPayloadType:   tool.MethodPayloadTypeRef,
			RuntimeToolName:     tool.QualifiedName,
			DispatcherFuncName:  "Dispatch" + tool.ConstName + "Method",
			DispatchOptionsName: tool.ConstName + "DispatchOptions",
			QualifiedSourceTool: tool.QualifiedName,
			HasPayload:          tool.Args != nil && tool.Args.Type != expr.Empty,
			HasResult:           tool.HasResult,
			InputSchema:         inputSchema,
			OutputSchema:        outputSchema,
			ExampleArguments:    buildExampleJSON(tool.Args),
		})
	}
	return out, nil
}

func hasAgentRoot(roots []eval.Root) bool {
	for _, root := range roots {
		if _, ok := root.(*agentsexpr.RootExpr); ok {
			return true
		}
	}
	return false
}

// generateMCPServiceCode generates the MCP service layer and JSON-RPC transport
// using Goa's built-in generators.
func generateMCPServiceCode(genpkg string, root *expr.RootExpr, mcpService *expr.ServiceExpr, protocolVersion string) ([]*codegen.File, error) {
	files := make([]*codegen.File, 0, 16)

	// Create services data from temporary MCP root
	servicesData := service.NewServicesData(root)

	// Generate MCP service layer only (no HTTP transports for original service)
	userTypePkgs := make(map[string][]string)
	serviceFiles := service.Files(genpkg, mcpService, servicesData, userTypePkgs)
	for _, f := range serviceFiles {
		if strings.HasSuffix(filepath.ToSlash(f.Path), "/service.go") {
			service.AddServiceDataMetaTypeImports(f.HeaderTemplate(), servicesData.Get(mcpService.Name))
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

	if err := applyMCPPolicyHeadersToJSONRPCMount(files, protocolVersion); err != nil {
		return nil, err
	}
	if err := applyMCPJSONRPCErrorCodes(files); err != nil {
		return nil, err
	}
	return files, nil
}

// applyMCPPolicyHeadersToJSONRPCMount replaces the JSON-RPC server mount section
// with a loom-mcp-owned template that propagates MCP policy headers into the
// request context.
//
// This avoids any string-based patching while ensuring header-driven allow/deny
// policy can be enforced by MCP adapters without requiring example/server wiring
// changes.
func applyMCPPolicyHeadersToJSONRPCMount(files []*codegen.File, protocolVersion string) error {
	for _, f := range files {
		if f == nil {
			continue
		}
		if filepath.Base(filepath.Dir(filepath.ToSlash(f.Path))) != "server" || filepath.Base(f.Path) != "server.go" {
			continue
		}
		rewritten, err := rewriteJSONRPCServerFile(f, protocolVersion)
		if err != nil {
			return err
		}
		if !rewritten {
			return fmt.Errorf("upstream JSON-RPC mount shape changed in %s: expected to wrap at least one mount handler with MCP policy headers", filepath.ToSlash(f.Path))
		}
		if header := f.HeaderTemplate(); header != nil {
			codegen.AddImport(header, &codegen.ImportSpec{Path: "bytes"})
			codegen.AddImport(header, &codegen.ImportSpec{Path: "encoding/json"})
			codegen.AddImport(header, &codegen.ImportSpec{Path: "fmt"})
			codegen.AddImport(header, &codegen.ImportSpec{Path: "io"})
			codegen.AddImport(header, &codegen.ImportSpec{Path: "github.com/CaliLuke/loom-mcp/runtime/mcp", Name: "mcpruntime"})
		}
	}
	return nil
}

func rewriteJSONRPCServerFile(file *codegen.File, protocolVersion string) (bool, error) {
	sections := file.AllSections()
	if len(sections) == 0 {
		return false, nil
	}
	updated := make([]codegen.Section, 0, len(sections))
	rewritten := false
	for _, section := range sections {
		next, ok, err := rewriteJSONRPCServerSection(section, protocolVersion)
		if err != nil {
			return false, err
		}
		rewritten = rewritten || ok
		updated = append(updated, next)
	}
	file.SetSections(updated)
	return rewritten, nil
}

func rewriteJSONRPCServerSection(section codegen.Section, protocolVersion string) (codegen.Section, bool, error) {
	switch sec := section.(type) {
	case *codegen.SectionTemplate:
		if sec == nil {
			return nil, false, nil
		}
		if rewritten, ok, err := rewriteJSONRPCSectionByRenderedSource(sec, protocolVersion); ok || err != nil {
			return rewritten, ok, err
		}
		return sec, false, nil
	case *codegen.RawSection, *codegen.JenniferSection:
		if rewritten, ok, err := rewriteJSONRPCSectionByRenderedSource(sec, protocolVersion); ok || err != nil {
			return rewritten, ok, err
		}
	default:
		if rewritten, ok, err := rewriteJSONRPCSectionByRenderedSource(section, protocolVersion); ok || err != nil {
			return rewritten, ok, err
		}
	}
	return section, false, nil
}

func rewriteJSONRPCSectionByRenderedSource(section codegen.Section, protocolVersion string) (codegen.Section, bool, error) {
	if section == nil {
		return nil, false, nil
	}
	source, ok := renderedSectionSource(section)
	if !ok {
		return nil, false, nil
	}
	if section.SectionName() != jsonrpcServerMountSectionName && !isJSONRPCMountSource(source) {
		return nil, false, nil
	}
	rewritten, ok := rewriteJSONRPCServerMountSource(source, protocolVersion)
	if !ok {
		return nil, false, fmt.Errorf("upstream JSON-RPC mount shape changed in section %q: could not wrap any mount handler with MCP policy headers", section.SectionName())
	}
	return &codegen.RawSection{
		Name:   section.SectionName(),
		Source: rewritten,
	}, true, nil
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

func rewriteJSONRPCServerMountSource(source string, protocolVersion string) (string, bool) {
	if source == "" {
		return source, false
	}

	updated := source
	updated = strings.ReplaceAll(updated, ", h.ServeHTTP)\n", ", withMCPPolicyHeaders(h.ServeHTTP))\n")
	updated = strings.ReplaceAll(updated, ", h.handleSSE)\n", ", withMCPPolicyHeaders(h.handleSSE))\n")
	if updated == source {
		return source, false
	}

	if strings.Contains(updated, "Mixed transports:") {
		updated = addMixedTransportSessionRoutes(updated)
	}
	if strings.Contains(updated, "func withMCPPolicyHeaders(") {
		return updated, true
	}
	return strings.TrimRight(updated, "\n") + jsonrpcServerMountHelperSource(protocolVersion), true
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

func applyMCPJSONRPCErrorCodes(files []*codegen.File) error {
	for _, f := range files {
		if f == nil {
			continue
		}
		if filepath.Base(filepath.Dir(filepath.ToSlash(f.Path))) != "server" || filepath.Base(f.Path) != "server.go" {
			continue
		}
		rewritten := rewriteJSONRPCResourceNotFoundFile(f)
		if !rewritten {
			return fmt.Errorf("upstream JSON-RPC error mapping shape changed in %s: expected to add MCP resource_not_found mapping", filepath.ToSlash(f.Path))
		}
	}
	return nil
}

func rewriteJSONRPCResourceNotFoundFile(file *codegen.File) bool {
	sections := file.AllSections()
	if len(sections) == 0 {
		return false
	}
	updated := make([]codegen.Section, 0, len(sections))
	rewritten := false
	for _, section := range sections {
		next, ok := rewriteJSONRPCResourceNotFoundSection(section)
		rewritten = rewritten || ok
		updated = append(updated, next)
	}
	file.SetSections(updated)
	return rewritten
}

func rewriteJSONRPCResourceNotFoundSection(section codegen.Section) (codegen.Section, bool) {
	source, ok := renderedSectionSource(section)
	if !ok {
		return section, false
	}
	rewritten, ok := rewriteJSONRPCResourceNotFoundSource(source)
	if !ok {
		return section, false
	}
	return &codegen.RawSection{
		Name:   section.SectionName(),
		Source: rewritten,
	}, true
}

func rewriteJSONRPCResourceNotFoundSource(source string) (string, bool) {
	if source == "" || strings.Contains(source, `case "resource_not_found":`) {
		return source, false
	}

	updated := strings.ReplaceAll(source,
		`case "method_not_found":
					encodeJSONRPCError(ctx, w, req, jsonrpc.MethodNotFound, loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err), encoder, errhandler)`,
		`case "resource_not_found":
					encodeJSONRPCError(ctx, w, req, jsonrpc.Code(-32002), loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err), encoder, errhandler)
				case "method_not_found":
					encodeJSONRPCError(ctx, w, req, jsonrpc.MethodNotFound, loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err), encoder, errhandler)`)
	updated = strings.ReplaceAll(updated,
		`case "method_not_found":
	encodeJSONRPCError(ctx, w, req, jsonrpc.MethodNotFound, loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err), encoder, errhandler)`,
		`case "resource_not_found":
	encodeJSONRPCError(ctx, w, req, jsonrpc.Code(-32002), loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err), encoder, errhandler)
case "method_not_found":
	encodeJSONRPCError(ctx, w, req, jsonrpc.MethodNotFound, loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err), encoder, errhandler)`)
	updated = strings.ReplaceAll(updated,
		`case "method_not_found":
						return strm.sendError(ctx, jsonrpc.IDToString(req.ID), jsonrpc.MethodNotFound, loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err))`,
		`case "resource_not_found":
						return strm.sendError(ctx, jsonrpc.IDToString(req.ID), jsonrpc.Code(-32002), loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err))
					case "method_not_found":
						return strm.sendError(ctx, jsonrpc.IDToString(req.ID), jsonrpc.MethodNotFound, loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err))`)
	updated = strings.ReplaceAll(updated,
		`case "method_not_found":
	return strm.sendError(ctx, jsonrpc.IDToString(req.ID), jsonrpc.MethodNotFound, loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err))`,
		`case "resource_not_found":
	return strm.sendError(ctx, jsonrpc.IDToString(req.ID), jsonrpc.Code(-32002), loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err))
case "method_not_found":
	return strm.sendError(ctx, jsonrpc.IDToString(req.ID), jsonrpc.MethodNotFound, loom.ErrorSafeMessage(err), jsonrpc.NewErrorData(err))`)
	if updated == source {
		return source, false
	}
	return updated, true
}

func jsonrpcServerMountHelperSource(protocolVersion string) string {
	return fmt.Sprintf(`

// withMCPPolicyHeaders propagates MCP policy header values into the request context.
//
// The MCP adapter enforces resource allow/deny policies based on context values:
//   - allowed resource names (CSV list of resource names)
//   - denied resource names  (CSV list of resource names)
//
// This helper maps those values from the corresponding HTTP headers:
//   - x-mcp-allow-names
//   - x-mcp-deny-names
//
// It is installed by the JSON-RPC Mount functions so consumers do not need
// to patch example servers or wire middleware manually.
func withMCPPolicyHeaders(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := validateMCPProtocolVersionHeader(r); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"error": map[string]any{
					"code":    -32602,
					"message": err.Error(),
				},
			})
			return
		}
		if acceptedMCPJSONRPCNotificationOrResponse(r) {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		ctx := r.Context()
		if allow := r.Header.Get("x-mcp-allow-names"); allow != "" {
			ctx = mcpruntime.WithAllowedResourceNames(ctx, allow)
		}
		if deny := r.Header.Get("x-mcp-deny-names"); deny != "" {
			ctx = mcpruntime.WithDeniedResourceNames(ctx, deny)
		}
		if sessionID := r.Header.Get(mcpruntime.HeaderKeySessionID); sessionID != "" {
			ctx = mcpruntime.WithSessionID(ctx, sessionID)
		}
		ctx = mcpruntime.WithResponseWriter(ctx, w)
		next(w, r.WithContext(ctx))
	}
}

func validateMCPProtocolVersionHeader(r *http.Request) error {
	if r == nil {
		return nil
	}
	method := ""
	if r.Method == http.MethodPost {
		parsed, err := jsonRPCRequestMethod(r)
		if err != nil {
			return err
		}
		method = parsed
	}
	if method == "initialize" {
		return nil
	}
	version := r.Header.Get(mcpruntime.HeaderKeyProtocolVersion)
	if version == "" {
		if r.Header.Get(mcpruntime.HeaderKeySessionID) == "" {
			return nil
		}
		return fmt.Errorf("Missing %%s header", mcpruntime.HeaderKeyProtocolVersion)
	}
	for _, supported := range []string{%s} {
		if version == supported {
			return nil
		}
	}
	return fmt.Errorf("Unsupported %%s header %%q", mcpruntime.HeaderKeyProtocolVersion, version)
}

func jsonRPCRequestMethod(r *http.Request) (string, error) {
	if r == nil || r.Body == nil {
		return "", nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return "", err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) == 0 {
		return "", nil
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", nil
	}
	method, _ := envelope["method"].(string)
	return method, nil
}

func acceptedMCPJSONRPCNotificationOrResponse(r *http.Request) bool {
	if r == nil || r.Method != http.MethodPost || r.Body == nil {
		return false
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) == 0 {
		return false
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	if envelope["jsonrpc"] != "2.0" {
		return false
	}
	method, _ := envelope["method"].(string)
	if method == "" {
		_, hasResult := envelope["result"]
		_, hasError := envelope["error"]
		return hasResult || hasError
	}
	if _, hasID := envelope["id"]; hasID {
		return false
	}
	switch method {
	case "notifications/initialized", "notifications/cancelled", "notifications/progress", "notifications/roots/list_changed":
		return true
	default:
		return false
	}
}
`, supportedProtocolVersionLiterals(protocolVersion))
}

func supportedProtocolVersionLiterals(protocolVersion string) string {
	supported := supportedProtocolVersions(protocolVersion)
	quoted := make([]string, 0, len(supported))
	for _, version := range supported {
		quoted = append(quoted, fmt.Sprintf("%q", version))
	}
	return strings.Join(quoted, ", ")
}

func supportedProtocolVersions(protocolVersion string) []string {
	pv := defaultProtocolVersion(protocolVersion)
	supported := []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}
	if !slices.Contains(supported, pv) {
		supported = append([]string{pv}, supported...)
	}
	return supported
}

// generateMCPTransport generates adapter and prompt provider files that adapt
// MCP protocol methods to the original service implementation.
func generateMCPTransport(genpkg string, svc *expr.ServiceExpr, data *AdapterData) []*codegen.File {
	var files []*codegen.File
	svcName := codegen.SnakeCase(svc.Name)

	pkgName := data.MCPPackage
	files = append(files, buildMCPAdapterFile(genpkg, svc, data, svcName))
	files = append(files, buildMCPProtocolVersionFile(pkgName, svcName, data.ProtocolVersion))
	if discovery := oauthDiscoveryFile(data); discovery != nil {
		files = append(files, discovery)
	}
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
			codegen.Header(fmt.Sprintf("MCP server adapter for %s service", svc.Name), data.MCPPackage, adapterImports(genpkg, svc, svcName, data)),
			adapterCoreSection(data),
			adapterBroadcastSection(),
			adapterToolsSection(data),
			adapterResourcesSection(data),
			adapterPromptsSection(data),
			adapterNotificationsSection(),
			adapterSubscriptionsSection(data),
		},
	}
}

func adapterImports(genpkg string, svc *expr.ServiceExpr, svcName string, data *AdapterData) []*codegen.ImportSpec {
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
		{Path: "regexp"},
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
		{Path: upstreampaths.LoomPkgImportPath, Name: "loom"},
	}...)
	if len(data.Tools) > 0 {
		imports = append(imports, &codegen.ImportSpec{Path: "github.com/sahilm/fuzzy"})
	}
	if len(data.SkillDirectories) > 0 {
		imports = append(imports, &codegen.ImportSpec{
			Path: "github.com/CaliLuke/loom-mcp/runtime/mcp/skills",
			Name: "mcpskills",
		})
	}
	if adapterDataHasProjectedTools(data) {
		imports = append(imports, &codegen.ImportSpec{
			Path: "github.com/CaliLuke/loom-mcp/runtime/agent/runtime",
			Name: "agentruntime",
		})
		seenProjectedImports := map[string]struct{}{}
		for _, tool := range data.Tools {
			if tool == nil || tool.Projected == nil || tool.Projected.SpecsImportPath == "" {
				continue
			}
			if _, ok := seenProjectedImports[tool.Projected.SpecsImportPath]; ok {
				continue
			}
			seenProjectedImports[tool.Projected.SpecsImportPath] = struct{}{}
			imports = append(imports, &codegen.ImportSpec{
				Path: tool.Projected.SpecsImportPath,
				Name: tool.Projected.SpecsPackageName,
			})
		}
	}
	return append(imports, adapterAttributeImports(genpkg, svc, imports)...)
}

func adapterDataHasProjectedTools(data *AdapterData) bool {
	if data == nil {
		return false
	}
	for _, tool := range data.Tools {
		if tool != nil && tool.Projected != nil {
			return true
		}
	}
	return false
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
	supported := supportedProtocolVersions(protocolVersion)
	pv := defaultProtocolVersion(protocolVersion)
	var list strings.Builder
	for _, v := range supported {
		fmt.Fprintf(&list, "\t%q,\n", v)
	}
	source := fmt.Sprintf("const DefaultProtocolVersion = %q\n\nvar SupportedProtocolVersions = []string{\n%s}\n", pv, list.String())
	return &codegen.File{
		Path: filepath.Join(codegen.Gendir, "mcp_"+svcName, "protocol_version.go"),
		SectionTemplates: []*codegen.SectionTemplate{
			codegen.Header("MCP protocol version", pkgName, nil),
			{Name: "mcp-protocol-version", Source: source},
		},
	}
}

func defaultProtocolVersion(protocolVersion string) string {
	if protocolVersion != "" {
		return protocolVersion
	}
	return "2025-11-25"
}

func buildMCPPromptProviderFile(genpkg string, svc *expr.ServiceExpr, data *AdapterData, svcName, pkgName string) *codegen.File {
	if len(data.StaticPrompts) == 0 && len(data.DynamicPrompts) == 0 {
		return nil
	}
	imports := []*codegen.ImportSpec{
		{Path: "context"},
		{Path: "encoding/json"},
		{Path: genpkg + "/" + svcName, Name: svcName},
	}
	if hasRuntimePrompts(data.StaticPrompts) {
		imports = append(imports, &codegen.ImportSpec{
			Path: "github.com/CaliLuke/loom-mcp/runtime/agent/prompt",
		})
	}
	return &codegen.File{
		Path: filepath.Join(codegen.Gendir, "mcp_"+svcName, "prompt_provider.go"),
		Sections: []codegen.Section{
			codegen.Header(fmt.Sprintf("MCP prompt provider for %s service", svc.Name), pkgName, imports),
			promptProviderSection(data),
		},
	}
}
