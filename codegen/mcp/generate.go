package codegen

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
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

// mcpDecoderPayloadVarPattern locates the payload variable declaration inside
// generated JSON-RPC request decoders so rewrites can identify the decoded
// payload type without depending on decoder function naming.
var mcpDecoderPayloadVarPattern = regexp.MustCompile(`\bvar payload \*\w+\.(\w+)`)

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

		// Resolve projected toolset tools first: the expression builder must
		// know about them so tools/list + tools/call methods and types exist
		// for MCP servers whose only tools come from MCPPlacement projection.
		projected, err := ProjectedToolInventory(genpkg, roots, svc.Name, mcp.Name)
		if err != nil {
			return nil, fmt.Errorf("build projected tool inventory for %s.%s: %w", svc.Name, mcp.Name, err)
		}

		// Build MCP service expression
		exprBuilder := newMCPExprBuilder(svc, mcp, source, len(projected))
		mcpService := exprBuilder.BuildServiceExpr()

		// Create temporary root for MCP generation
		mcpRoot := exprBuilder.BuildRootExpr(mcpService)

		// Prepare, validate, and finalize MCP expressions
		if err := exprBuilder.PrepareAndValidate(mcpRoot); err != nil {
			return nil, fmt.Errorf("MCP expression validation failed: %w", err)
		}

		// Build mapping and adapter data early so we can customize generated clients
		mapping := exprBuilder.BuildServiceMapping()
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
			ExampleArguments:    synthesizeCanonicalExample(tool.Args),
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
	if err := applyMCPJSONRPCOptionalParamsDecoding(files, mcpService); err != nil {
		return nil, err
	}
	if err := applyMCPJSONRPCStreamFinalEventName(files); err != nil {
		return nil, err
	}
	if err := applyMCPJSONRPCClientTransportDefaults(files); err != nil {
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
			codegen.AddImport(header, &codegen.ImportSpec{Path: "errors"})
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
		(strings.Contains(source, "h.Handler.ServeHTTP") || strings.Contains(source, "h.ServeHTTP") || strings.Contains(source, "h.handleSSE"))
}

func rewriteJSONRPCServerMountSource(source string, protocolVersion string) (string, bool) {
	if source == "" {
		return source, false
	}

	updated := source
	// loom >= v1.3.0 mounts h.Handler.ServeHTTP, where Handler wraps ServeHTTP
	// in an unconfigurable http.NewCrossOriginProtection. Mount the negotiating
	// ServeHTTP directly instead: withMCPPolicyHeaders already applies the
	// configurable MCPCrossOriginProtection, and stacking the upstream layer
	// would reject trusted cross-origin clients added via AddTrustedOrigin.
	updated = strings.ReplaceAll(updated, ", h.Handler.ServeHTTP)\n", ", withMCPPolicyHeaders(streamableHTTPSessions, requestCancellations, h.ServeHTTP))\n")
	// loom < v1.3.0 mount shapes.
	updated = strings.ReplaceAll(updated, ", h.ServeHTTP)\n", ", withMCPPolicyHeaders(streamableHTTPSessions, requestCancellations, h.ServeHTTP))\n")
	updated = strings.ReplaceAll(updated, ", h.handleSSE)\n", ", withMCPPolicyHeaders(streamableHTTPSessions, requestCancellations, h.handleSSE))\n")
	if updated == source {
		return source, false
	}
	registered, ok := addMCPTransportState(updated)
	if !ok {
		return source, false
	}
	updated = registered

	if strings.Contains(updated, "Mixed transports:") {
		routed, ok := addMixedTransportSessionRoutes(updated)
		if !ok {
			// The mount function shape drifted (issue #148 was exactly this:
			// the goahttp -> loomhttp rename silently disabled the session
			// route insertion). Refuse to emit rather than lose the routes.
			return source, false
		}
		updated = routed
	}
	if strings.Contains(updated, "func withMCPPolicyHeaders(") {
		return updated, true
	}
	return strings.TrimRight(updated, "\n") + jsonrpcServerMountHelperSource(protocolVersion), true
}

// addMCPTransportState creates shared session and request-cancellation state
// for all routes mounted by a generated MCP server.
func addMCPTransportState(source string) (string, bool) {
	lines := strings.Split(source, "\n")
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "func Mount") || !strings.Contains(trimmed, "(mux ") || !strings.HasSuffix(trimmed, "{") {
			continue
		}
		updated := make([]string, 0, len(lines)+2)
		updated = append(updated, lines[:idx+1]...)
		updated = append(updated,
			"\tstreamableHTTPSessions := mcpruntime.NewStreamableHTTPSessions()",
			"\trequestCancellations := mcpruntime.NewRequestCancellationRegistry()",
		)
		updated = append(updated, lines[idx+1:]...)
		return strings.Join(updated, "\n"), true
	}
	return source, false
}

// addMixedTransportSessionRoutes inserts the streamable HTTP session routes
// (currently DELETE for client session termination; GET on older templates)
// that the upstream mixed-transport mount does not emit. It reports ok=false
// when the mount function cannot be located, so callers fail fast instead of
// silently shipping a server without session routes.
func addMixedTransportSessionRoutes(source string) (string, bool) {
	lines := strings.Split(source, "\n")
	insertAt := -1
	paths := make([]string, 0, 1)
	seenPaths := make(map[string]struct{})
	seenMethods := make(map[string]map[string]struct{})
	inMount := false

	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Match the mount func regardless of the muxer package qualifier
		// (loomhttp today, goahttp historically, http in hand-built sections).
		if strings.HasPrefix(trimmed, "func ") && strings.Contains(trimmed, "(mux ") && strings.Contains(trimmed, ".Muxer, h *") {
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
		return source, false
	}

	extra := make([]string, 0, len(paths)*2)
	for _, path := range paths {
		for _, method := range []string{"GET", "DELETE"} {
			if _, ok := seenMethods[path][method]; ok {
				continue
			}
			extra = append(extra, fmt.Sprintf("\tmux.Handle(%q, %q, withMCPPolicyHeaders(streamableHTTPSessions, requestCancellations, h.ServeHTTP))", method, path))
		}
	}
	if len(extra) == 0 {
		return source, true
	}

	updated := make([]string, 0, len(lines)+len(extra))
	updated = append(updated, lines[:insertAt]...)
	updated = append(updated, extra...)
	updated = append(updated, lines[insertAt:]...)
	return strings.Join(updated, "\n"), true
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

// applyMCPJSONRPCOptionalParamsDecoding rewrites generated JSON-RPC request
// decoders so MCP methods whose params are entirely optional (tools/list,
// resources/list, prompts/list, ...) accept requests that omit the "params"
// key. The MCP specification declares those params optional and official
// clients omit the key entirely, but the upstream loom jsonrpc server request
// decoder template (loom/jsonrpc/codegen encode_decode sections) maps the
// resulting io.EOF to loom.MissingPayloadError(), which surfaces as a -32602
// invalid params error on the wire.
func applyMCPJSONRPCOptionalParamsDecoding(files []*codegen.File, mcpService *expr.ServiceExpr) error {
	targets := mcpAllOptionalPayloadTypeNames(mcpService)
	if len(targets) == 0 {
		return nil
	}
	remaining := make(map[string]struct{}, len(targets))
	for name := range targets {
		remaining[name] = struct{}{}
	}
	for _, f := range files {
		if f == nil {
			continue
		}
		if filepath.Base(filepath.Dir(filepath.ToSlash(f.Path))) != "server" || filepath.Base(f.Path) != "encode_decode.go" {
			continue
		}
		sections := f.AllSections()
		updated := make([]codegen.Section, 0, len(sections))
		for _, section := range sections {
			next, rewrittenType := rewriteMCPOptionalParamsSection(section, targets)
			if rewrittenType != "" {
				delete(remaining, rewrittenType)
			}
			updated = append(updated, next)
		}
		f.SetSections(updated)
	}
	if len(remaining) > 0 {
		missing := make([]string, 0, len(remaining))
		for name := range remaining {
			missing = append(missing, name)
		}
		sort.Strings(missing)
		return fmt.Errorf(
			"upstream JSON-RPC decoder shape changed: could not rewrite optional params decoding for %s",
			strings.Join(missing, ", "),
		)
	}
	return nil
}

// mcpAllOptionalPayloadTypeNames returns the user type names of MCP method
// payloads whose top-level object declares no required fields, i.e. payloads
// that are spec-legal to omit entirely on the wire.
func mcpAllOptionalPayloadTypeNames(mcpService *expr.ServiceExpr) map[string]struct{} {
	names := make(map[string]struct{})
	if mcpService == nil {
		return names
	}
	for _, m := range mcpService.Methods {
		if m == nil || m.Payload == nil || m.Payload.Type == nil || m.Payload.Type == expr.Empty {
			continue
		}
		ut, ok := m.Payload.Type.(expr.UserType)
		if !ok {
			continue
		}
		att := ut.Attribute()
		if att == nil || expr.AsObject(att.Type) == nil {
			continue
		}
		if att.Validation != nil && len(att.Validation.Required) > 0 {
			continue
		}
		names[ut.Name()] = struct{}{}
	}
	return names
}

// rewriteMCPOptionalParamsSection rewrites one decoder section when it decodes
// an all-optional MCP payload. It returns the (possibly replaced) section and
// the payload type name that was rewritten, or "" when the section was left
// untouched.
func rewriteMCPOptionalParamsSection(section codegen.Section, targets map[string]struct{}) (codegen.Section, string) {
	source, ok := renderedSectionSource(section)
	if !ok {
		return section, ""
	}
	match := mcpDecoderPayloadVarPattern.FindStringSubmatch(source)
	if match == nil {
		return section, ""
	}
	typeName := match[1]
	if _, ok := targets[typeName]; !ok {
		return section, ""
	}
	const eofBlock = "\t\t\tif errors.Is(err, io.EOF) {\n" +
		"\t\t\t\treturn payload, loom.MissingPayloadError()\n" +
		"\t\t\t}"
	if !strings.Contains(source, eofBlock) {
		return section, ""
	}
	replacement := "\t\t\tif errors.Is(err, io.EOF) {\n" +
		"\t\t\t\t// MCP declares these params optional; official clients omit the\n" +
		"\t\t\t\t// \"params\" key entirely. Decode absence as {}.\n" +
		"\t\t\t\treturn New" + typeName + "(&body), nil\n" +
		"\t\t\t}"
	return &codegen.RawSection{
		Name:   section.SectionName(),
		Source: strings.Replace(source, eofBlock, replacement, 1),
	}, typeName
}

// applyMCPJSONRPCStreamFinalEventName renames the SSE event used for final
// JSON-RPC responses on generated server streams from "response" to "message".
// SSE consumers only process default/"message" events for MCP streamable HTTP
// (the official go-sdk skips named events other than "message"), so emitting
// the final tools/call result as "response" makes conformant clients hang.
// Upstream origin: loom/jsonrpc/codegen/stream_sections.go (SendAndClose);
// fixed upstream in loom v1.3.0 (a35fa2c), so on current loom this is a
// verification pass: emitted streams that already use "message" are accepted
// unchanged, and the rewrite only fires for older "response"-emitting
// templates. A stream.go with neither pattern signals real template drift.
func applyMCPJSONRPCStreamFinalEventName(files []*codegen.File) error {
	for _, f := range files {
		if f == nil {
			continue
		}
		if filepath.Base(filepath.Dir(filepath.ToSlash(f.Path))) != "server" || filepath.Base(f.Path) != "stream.go" {
			continue
		}
		sections := f.AllSections()
		updated := make([]codegen.Section, 0, len(sections))
		rewritten := false
		conformant := false
		for _, section := range sections {
			source, ok := renderedSectionSource(section)
			if ok && strings.Contains(source, `sendSSEEvent("message", `) {
				conformant = true
			}
			if !ok || !strings.Contains(source, `sendSSEEvent("response", `) {
				updated = append(updated, section)
				continue
			}
			rewritten = true
			updated = append(updated, &codegen.RawSection{
				Name:   section.SectionName(),
				Source: strings.ReplaceAll(source, `sendSSEEvent("response", `, `sendSSEEvent("message", `),
			})
		}
		if !rewritten {
			if conformant {
				continue
			}
			return fmt.Errorf(
				"upstream JSON-RPC stream shape changed in %s: expected SendAndClose to emit a %q or %q SSE event",
				filepath.ToSlash(f.Path),
				"message",
				"response",
			)
		}
		f.SetSections(updated)
	}
	return nil
}

// applyMCPJSONRPCClientTransportDefaults hardens the generated JSON-RPC client
// so it satisfies the MCP streamable HTTP client requirements (2025-11-25
// transports spec):
//
//   - every request carries "Accept: application/json, text/event-stream"
//     (the upstream template only sets a bare "text/event-stream" on the
//     streaming endpoints and no Accept header anywhere else),
//   - the Mcp-Session-Id returned by initialize is captured and replayed on
//     all subsequent requests, together with the negotiated protocol version.
//
// Upstream origin: loom jsonrpc client template (loom/jsonrpc/codegen client
// files), which implements neither the Accept requirement nor session
// tracking. NewClient is rewritten to wrap the caller-provided Doer with the
// appended mcpClientDoer decorator.
func applyMCPJSONRPCClientTransportDefaults(files []*codegen.File) error {
	for _, f := range files {
		if f == nil {
			continue
		}
		if filepath.Base(filepath.Dir(filepath.ToSlash(f.Path))) != "client" || filepath.Base(f.Path) != "client.go" {
			continue
		}
		sections := f.AllSections()
		updated := make([]codegen.Section, 0, len(sections)+1)
		wrapped := false
		acceptRewritten := false
		for _, section := range sections {
			source, ok := renderedSectionSource(section)
			if !ok {
				updated = append(updated, section)
				continue
			}
			next := source
			if strings.Contains(next, `req.Header.Set("Accept", "text/event-stream")`) {
				next = strings.ReplaceAll(next,
					`req.Header.Set("Accept", "text/event-stream")`,
					`req.Header.Set("Accept", "application/json, text/event-stream")`)
				acceptRewritten = true
			}
			const newClientAnchor = ") *Client {\n\treturn &Client{"
			if strings.Contains(next, "func NewClient(") && strings.Contains(next, "doer ") && strings.Contains(next, newClientAnchor) {
				next = strings.Replace(next, newClientAnchor,
					") *Client {\n\tdoer = &mcpClientDoer{next: doer}\n\treturn &Client{", 1)
				wrapped = true
			}
			if next == source {
				updated = append(updated, section)
				continue
			}
			updated = append(updated, &codegen.RawSection{
				Name:   section.SectionName(),
				Source: next,
			})
		}
		if !wrapped {
			return fmt.Errorf("upstream JSON-RPC client shape changed in %s: could not wrap NewClient Doer with MCP transport defaults", filepath.ToSlash(f.Path))
		}
		if !acceptRewritten {
			return fmt.Errorf("upstream JSON-RPC client shape changed in %s: expected streaming Accept headers to rewrite", filepath.ToSlash(f.Path))
		}
		updated = append(updated, &codegen.RawSection{
			Name:   "mcp-client-transport-defaults",
			Source: mcpClientDoerSource,
		})
		f.SetSections(updated)
		if header := f.HeaderTemplate(); header != nil {
			codegen.AddImport(header, &codegen.ImportSpec{Path: "encoding/json"})
		}
	}
	return nil
}

// mcpClientDoerSource is appended to generated JSON-RPC client files by
// applyMCPJSONRPCClientTransportDefaults. It implements the MCP streamable
// HTTP client transport obligations that the upstream loom jsonrpc client
// template does not cover: the mandatory Accept header, Mcp-Session-Id
// capture/replay, and the negotiated protocol version header.
const mcpClientDoerSource = `

// mcpClientDoer decorates the transport Doer so every generated JSON-RPC
// request satisfies the MCP streamable HTTP client requirements:
//   - every request carries "Accept: application/json, text/event-stream"
//   - the Mcp-Session-Id returned by initialize is replayed on all
//     subsequent requests
//   - the negotiated MCP protocol version accompanies the session id
//
// NewClient installs this decorator so generated clients are protocol
// conformant without hand-written transport wrappers.
type mcpClientDoer struct {
	next interface {
		Do(*http.Request) (*http.Response, error)
	}

	mu              sync.Mutex
	sessionID       string
	protocolVersion string
}

// Do implements the transport Doer contract for mcpClientDoer.
func (d *mcpClientDoer) Do(req *http.Request) (*http.Response, error) {
	method, requestedVersion, err := mcpJSONRPCRequestInfo(req)
	if err != nil {
		return nil, err
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	if method != "initialize" {
		d.mu.Lock()
		sessionID, protocolVersion := d.sessionID, d.protocolVersion
		d.mu.Unlock()
		if sessionID != "" && req.Header.Get("Mcp-Session-Id") == "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}
		if protocolVersion != "" && req.Header.Get("MCP-Protocol-Version") == "" {
			req.Header.Set("MCP-Protocol-Version", protocolVersion)
		}
	}
	resp, err := d.next.Do(req)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if sessionID := resp.Header.Get("Mcp-Session-Id"); sessionID != "" {
		d.sessionID = sessionID
	}
	if method == "initialize" {
		if negotiated := mcpNegotiatedProtocolVersion(resp); negotiated != "" {
			d.protocolVersion = negotiated
		} else if requestedVersion != "" {
			d.protocolVersion = requestedVersion
		}
	}
	d.mu.Unlock()
	return resp, nil
}

// mcpJSONRPCRequestInfo peeks at the JSON-RPC request envelope without
// consuming the request body.
func mcpJSONRPCRequestInfo(req *http.Request) (string, string, error) {
	if req == nil || req.Body == nil {
		return "", "", nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return "", "", err
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) == 0 {
		return "", "", nil
	}
	var envelope struct {
		Method string ` + "`json:\"method\"`" + `
		Params struct {
			ProtocolVersion string ` + "`json:\"protocolVersion\"`" + `
		} ` + "`json:\"params\"`" + `
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", "", nil
	}
	return envelope.Method, envelope.Params.ProtocolVersion, nil
}

// mcpNegotiatedProtocolVersion extracts result.protocolVersion from an
// initialize response while restoring the body for downstream decoding.
func mcpNegotiatedProtocolVersion(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		return ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	if closeErr := resp.Body.Close(); closeErr != nil {
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return ""
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var envelope struct {
		Result struct {
			ProtocolVersion string ` + "`json:\"protocolVersion\"`" + `
		} ` + "`json:\"result\"`" + `
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	return envelope.Result.ProtocolVersion
}
`

func jsonrpcServerMountHelperSource(protocolVersion string) string {
	return fmt.Sprintf(`

// MCPMaxRequestBodyBytes limits JSON-RPC request bodies before middleware
// inspection. Set it to a positive number before mounting the server to choose
// a different bound; non-positive values disable the limit.
var MCPMaxRequestBodyBytes int64 = 32 << 20

// MCPCrossOriginProtection validates the Origin header on the generated MCP
// JSON-RPC transport to protect against DNS rebinding attacks, as required by
// the MCP streamable HTTP transport specification (2025-11-25, Security
// Warning: servers MUST validate Origin and respond 403 when it is present
// and invalid). The default allows same-origin and non-browser requests only,
// mirroring the http.NewCrossOriginProtection default that the generated SDK
// transport applies through mcpsdk.StreamableHTTPOptions.CrossOriginProtection.
// Call AddTrustedOrigin to allow additional origins, or set the variable to
// nil to disable the check, before mounting the server.
var MCPCrossOriginProtection = http.NewCrossOriginProtection()

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
// It also enforces the MCP transport Origin validation through
// MCPCrossOriginProtection before any request processing.
//
// It is installed by the JSON-RPC Mount functions so consumers do not need
// to patch example servers or wire middleware manually.
func withMCPPolicyHeaders(streamableHTTPSessions *mcpruntime.StreamableHTTPSessions, requestCancellations *mcpruntime.RequestCancellationRegistry, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if MCPCrossOriginProtection != nil {
			if err := MCPCrossOriginProtection.Check(r); err != nil {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
		}
		envelope, err := mcpJSONRPCEnvelopeFromRequest(w, r)
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "failed to read request", http.StatusBadRequest)
			return
		}
		if err := validateMCPProtocolVersionHeader(r, envelope); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      mcpJSONRPCResponseID(envelope),
				"error": map[string]any{
					"code":    -32602,
					"message": err.Error(),
				},
			})
			return
		}
		method := jsonRPCRequestMethod(envelope)
		if r.Method == http.MethodDelete {
			handleMCPStreamableHTTPSessionDelete(streamableHTTPSessions, w, r)
			return
		}
		if err := validateMCPStreamableHTTPSession(streamableHTTPSessions, r, method); err != nil {
			writeMCPStreamableHTTPSessionError(w, r, err)
			return
		}
		if acceptedMCPJSONRPCNotificationOrResponse(requestCancellations, r, envelope) {
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
		sessionID := r.Header.Get(mcpruntime.HeaderKeySessionID)
		if sessionID != "" {
			ctx = mcpruntime.WithSessionID(ctx, sessionID)
		}
		if requestID, ok := mcpJSONRPCRequestID(envelope); ok && sessionID != "" {
			requestCtx, cancel := context.WithCancel(ctx)
			cleanup := requestCancellations.Register(sessionID, requestID, cancel)
			defer cancel()
			defer cleanup()
			ctx = requestCtx
		}
		if r.Method == http.MethodGet {
			streamCtx, cancel := context.WithCancel(ctx)
			cleanup, err := streamableHTTPSessions.RegisterListener(sessionID, cancel)
			if err != nil {
				cancel()
				writeMCPStreamableHTTPSessionError(w, r, err)
				return
			}
			defer cancel()
			defer cleanup()
			ctx = streamCtx
		}
		ctx = mcpruntime.WithResponseWriter(ctx, w)
		responseWriter := w
		var responseObserver *mcpHTTPResponseObserver
		if mcpJSONRPCInputExpectsNoResponse(envelope) {
			responseObserver = &mcpHTTPResponseObserver{ResponseWriter: w}
			responseWriter = responseObserver
		}
		next(responseWriter, r.WithContext(ctx))
		if responseObserver != nil && !responseObserver.wroteResponse {
			w.WriteHeader(http.StatusAccepted)
		}
		if method == "initialize" {
			if issuedSessionID := w.Header().Get(mcpruntime.HeaderKeySessionID); issuedSessionID != "" {
				if err := streamableHTTPSessions.Issue(issuedSessionID); err != nil {
					writeMCPStreamableHTTPSessionError(w, r, err)
					return
				}
			}
		}
	}
}

func validateMCPStreamableHTTPSession(sessions *mcpruntime.StreamableHTTPSessions, r *http.Request, method string) error {
	if sessions == nil || r == nil {
		panic("streamable HTTP session validation requires a store and request")
	}
	if method == "initialize" {
		return nil
	}
	sessionID := r.Header.Get(mcpruntime.HeaderKeySessionID)
	if sessionID == "" {
		if !sessions.HasIssued() {
			return nil
		}
		return mcpruntime.ErrInvalidSessionID
	}
	return sessions.Validate(sessionID)
}

func handleMCPStreamableHTTPSessionDelete(sessions *mcpruntime.StreamableHTTPSessions, w http.ResponseWriter, r *http.Request) {
	if sessions == nil || r == nil {
		panic("streamable HTTP session termination requires a store and request")
	}
	sessionID := r.Header.Get(mcpruntime.HeaderKeySessionID)
	if sessionID == "" {
		http.Error(w, "Missing session ID", http.StatusBadRequest)
		return
	}
	if err := sessions.Terminate(sessionID); err != nil {
		writeMCPStreamableHTTPSessionError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func writeMCPStreamableHTTPSessionError(w http.ResponseWriter, r *http.Request, err error) {
	if r == nil || r.Header.Get(mcpruntime.HeaderKeySessionID) == "" {
		http.Error(w, "Missing session ID", http.StatusBadRequest)
		return
	}
	if errors.Is(err, mcpruntime.ErrInvalidSessionID) || errors.Is(err, mcpruntime.ErrSessionTerminated) {
		http.Error(w, "Invalid or expired session ID", http.StatusNotFound)
		return
	}
	http.Error(w, "Invalid or expired session ID", http.StatusNotFound)
}

func validateMCPProtocolVersionHeader(r *http.Request, envelope *mcpJSONRPCEnvelope) error {
	if r == nil {
		return nil
	}
	method := ""
	if r.Method == http.MethodPost {
		method = jsonRPCRequestMethod(envelope)
	}
	if method == "initialize" {
		return nil
	}
	version := r.Header.Get(mcpruntime.HeaderKeyProtocolVersion)
	if version == "" {
		// MCP clients using protocol versions before 2025-06-18 do not send this
		// header. The transport specification requires servers to assume the
		// 2025-03-26 compatibility version when no negotiated version is available.
		return nil
	}
	for _, supported := range []string{%s} {
		if version == supported {
			return nil
		}
	}
	return fmt.Errorf("Unsupported %%s header %%q", mcpruntime.HeaderKeyProtocolVersion, version)
}

func jsonRPCRequestMethod(envelope *mcpJSONRPCEnvelope) string {
	if envelope == nil {
		return ""
	}
	return envelope.Method
}

type mcpJSONRPCEnvelope struct {
	JSONRPC string          `+"`json:\"jsonrpc\"`"+`
	ID      json.RawMessage `+"`json:\"id\"`"+`
	Method  string          `+"`json:\"method\"`"+`
	Params  json.RawMessage `+"`json:\"params\"`"+`
	Result  json.RawMessage `+"`json:\"result\"`"+`
	Error   json.RawMessage `+"`json:\"error\"`"+`
	Batch   []*mcpJSONRPCEnvelope `+"`json:\"-\"`"+`
}

type mcpHTTPResponseObserver struct {
	http.ResponseWriter
	wroteResponse bool
}

func (w *mcpHTTPResponseObserver) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *mcpHTTPResponseObserver) WriteHeader(statusCode int) {
	w.wroteResponse = true
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *mcpHTTPResponseObserver) Write(data []byte) (int, error) {
	w.wroteResponse = true
	return w.ResponseWriter.Write(data)
}

func mcpJSONRPCEnvelopeFromRequest(w http.ResponseWriter, r *http.Request) (*mcpJSONRPCEnvelope, error) {
	if r == nil || r.Method != http.MethodPost || r.Body == nil {
		return nil, nil
	}
	reader := r.Body
	if MCPMaxRequestBodyBytes > 0 {
		reader = http.MaxBytesReader(w, r.Body, MCPMaxRequestBodyBytes)
	}
	body, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) == 0 {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var batch []*mcpJSONRPCEnvelope
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, nil
		}
		return &mcpJSONRPCEnvelope{Batch: batch}, nil
	}
	var envelope mcpJSONRPCEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, nil
	}
	return &envelope, nil
}

func mcpJSONRPCInputExpectsNoResponse(envelope *mcpJSONRPCEnvelope) bool {
	if envelope == nil {
		return false
	}
	if len(envelope.Batch) > 0 {
		for _, item := range envelope.Batch {
			if !mcpJSONRPCInputExpectsNoResponse(item) {
				return false
			}
		}
		return true
	}
	return envelope.JSONRPC == "2.0" && envelope.Method != "" && len(envelope.ID) == 0
}

func mcpJSONRPCRequestID(envelope *mcpJSONRPCEnvelope) (string, bool) {
	if envelope == nil || envelope.Method == "" {
		return "", false
	}
	return canonicalMCPJSONRPCRequestID(envelope.ID)
}

func mcpJSONRPCResponseID(envelope *mcpJSONRPCEnvelope) any {
	if envelope == nil || len(envelope.ID) == 0 || bytes.Equal(bytes.TrimSpace(envelope.ID), []byte("null")) {
		return nil
	}
	return json.RawMessage(envelope.ID)
}

func canonicalMCPJSONRPCRequestID(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", false
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "", false
	}
	return compact.String(), true
}

func acceptedMCPJSONRPCNotificationOrResponse(requestCancellations *mcpruntime.RequestCancellationRegistry, r *http.Request, envelope *mcpJSONRPCEnvelope) bool {
	if r == nil || r.Method != http.MethodPost {
		return false
	}
	if envelope == nil {
		return false
	}
	if envelope.JSONRPC != "2.0" {
		return false
	}
	if envelope.Method == "" {
		return len(envelope.Result) > 0 || len(envelope.Error) > 0
	}
	if len(envelope.ID) > 0 {
		return false
	}
	switch envelope.Method {
	case "notifications/cancelled":
		var params struct {
			RequestID json.RawMessage `+"`json:\"requestId\"`"+`
		}
		if err := json.Unmarshal(envelope.Params, &params); err == nil {
			if requestID, ok := canonicalMCPJSONRPCRequestID(params.RequestID); ok {
				requestCancellations.Cancel(r.Header.Get(mcpruntime.HeaderKeySessionID), requestID)
			}
		}
		return true
	case "notifications/initialized", "notifications/progress", "notifications/roots/list_changed":
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
			adapterNotificationsSection(data),
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
		{Path: "github.com/sahilm/fuzzy"},
		{Path: genpkg + "/" + svcName, Name: svcName},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/mcp", Name: "mcpruntime"},
		{Path: upstreampaths.LoomMCPHTTPImportPath, Name: "goahttp"},
		{Path: upstreampaths.LoomPkgImportPath, Name: "loom"},
	}...)
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
