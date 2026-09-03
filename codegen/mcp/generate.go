package codegen

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	agentcodegen "github.com/CaliLuke/loom-mcp/v2/codegen/agent"
	"github.com/CaliLuke/loom-mcp/v2/codegen/shared"
	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	"github.com/CaliLuke/loom-mcp/v2/internal/upstreampaths"
	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/codegen/service"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
)

// Generate orchestrates MCP code generation for services that declare MCP
// configuration in the DSL. It generates the internal adapter contract and an
// official-SDK server without synthesizing a native MCP wire transport.
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
		if provider := localProviderFile(adapterData); provider != nil {
			files = append(files, provider)
		}

		// Generate only the internal service types used by the adapter.
		mcpFiles := generateMCPServiceCode(genpkg, mcpRoot, mcpService)
		files = append(files, mcpFiles...)

		// Generate MCP transport that wraps the original service
		files = append(files, generateMCPTransport(genpkg, svc, adapterData)...)
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
		advertisedArgs := tool.Args
		if tool.Args != nil {
			advertisedArgs, err = agentcodegen.AdvertisedPayloadAttribute(tool.Args, tool.InjectedFields)
			if err != nil {
				return nil, fmt.Errorf("build advertised projected input for %q: %w", tool.QualifiedName, err)
			}
			schema, err := shared.ToJSONSchema(advertisedArgs)
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
			InjectedFields:      append([]string(nil), tool.InjectedFields...),
			HasBounds:           tool.Bounds != nil,
			InputSchema:         inputSchema,
			OutputSchema:        outputSchema,
			ExampleArguments:    synthesizeCanonicalExample(advertisedArgs),
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

// generateMCPServiceCode generates the minimal internal adapter service layer.
func generateMCPServiceCode(genpkg string, root *expr.RootExpr, mcpService *expr.ServiceExpr) []*codegen.File {
	files := make([]*codegen.File, 0, 1)

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
	return files
}

// generateMCPTransport generates adapter and prompt provider files that adapt
// MCP protocol methods to the original service implementation.
func generateMCPTransport(genpkg string, svc *expr.ServiceExpr, data *AdapterData) []*codegen.File {
	var files []*codegen.File
	svcName := codegen.SnakeCase(svc.Name)

	pkgName := data.MCPPackage
	files = append(files, buildMCPAdapterFile(genpkg, svc, data, svcName))
	if discovery := oauthDiscoveryFile(data); discovery != nil {
		files = append(files, discovery)
	}
	files = append(files, buildMCPSDKServerFile(genpkg, svc, data, svcName, pkgName))
	if provider := buildMCPPromptProviderFile(genpkg, svc, data, svcName, pkgName); provider != nil {
		files = append(files, provider)
	}
	return files
}

func buildMCPAdapterFile(genpkg string, svc *expr.ServiceExpr, data *AdapterData, svcName string) *codegen.File {
	adapterPath := filepath.Join(codegen.Gendir, "mcp_"+svcName, "adapter_server.go")
	return &codegen.File{
		Path: adapterPath,
		Sections: []codegen.Section{
			codegen.Header(fmt.Sprintf("MCP server adapter for %s service", svc.Name), data.MCPPackage, adapterImports(genpkg, svc, svcName, data)),
			adapterCoreSection(data),
			adapterToolsSection(data),
			adapterResourcesSection(data),
			adapterPromptsSection(data),
		},
	}
}

func adapterImports(genpkg string, svc *expr.ServiceExpr, svcName string, data *AdapterData) []*codegen.ImportSpec {
	imports := make([]*codegen.ImportSpec, 0, 24)
	imports = append(imports, []*codegen.ImportSpec{
		{Path: "bytes"},
		{Path: "context"},
		{Name: "json", Path: "encoding/json/v2"},
		{Name: "jsontext", Path: "encoding/json/jsontext"},
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
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/mcp", Name: "mcpruntime"},
		{Path: upstreampaths.LoomMCPHTTPImportPath, Name: "goahttp"},
		{Path: upstreampaths.LoomPkgImportPath, Name: "loom"},
	}...)
	if len(data.SkillDirectories) > 0 {
		imports = append(imports, &codegen.ImportSpec{
			Path: "github.com/CaliLuke/loom-mcp/v2/runtime/mcp/skills",
			Name: "mcpskills",
		})
	}
	if adapterDataHasProjectedTools(data) {
		imports = append(imports, &codegen.ImportSpec{
			Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime",
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

func buildMCPPromptProviderFile(genpkg string, svc *expr.ServiceExpr, data *AdapterData, svcName, pkgName string) *codegen.File {
	if len(data.StaticPrompts) == 0 && len(data.DynamicPrompts) == 0 {
		return nil
	}
	imports := []*codegen.ImportSpec{
		{Path: "context"},
		{Name: "json", Path: "encoding/json/v2"},
		{Name: "jsontext", Path: "encoding/json/jsontext"},
		{Path: genpkg + "/" + svcName, Name: svcName},
	}
	if hasRuntimePrompts(data.StaticPrompts) {
		imports = append(imports, &codegen.ImportSpec{
			Path: "github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt",
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
