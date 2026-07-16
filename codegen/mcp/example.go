package codegen

import (
	"fmt"
	"path/filepath"
	"strings"

	mcpexpr "github.com/CaliLuke/loom-mcp/expr/mcp"
	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/codegen/example"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
	"github.com/dave/jennifer/jen"
)

// PrepareExample augments the original roots so the Goa example generator
// includes the MCP JSON-RPC server without manual cmd edits. It runs the same
// pure-MCP contract validation as Generate so example scaffolding cannot mask
// invalid MCP mappings.
func PrepareExample(genpkg string, roots []eval.Root) error {
	source := collectSourceSnapshot(roots)
	for _, root := range roots {
		r, ok := root.(*expr.RootExpr)
		if !ok {
			continue
		}
		for _, svc := range r.Services {
			if !mcpexpr.Root.HasMCP(svc) {
				continue
			}
			mcp := mcpexpr.Root.GetMCP(svc)
			if err := validatePureMCPService(svc, mcp, source); err != nil {
				return err
			}
			projected, err := ProjectedToolInventory(genpkg, roots, svc.Name, mcp.Name)
			if err != nil {
				return fmt.Errorf("build projected tool inventory for %s.%s: %w", svc.Name, mcp.Name, err)
			}
			builder := newMCPExprBuilder(svc, mcp, source, len(projected))
			mcpService := builder.BuildServiceExpr()

			// Build and validate a temporary MCP root to finalize types
			mcpTempRoot := builder.BuildRootExpr(mcpService)
			if err := builder.PrepareAndValidate(mcpTempRoot); err != nil {
				return err
			}

			// Inject MCP into original root so example generation mounts it
			if r.API == nil {
				r.API = &expr.APIExpr{}
			}
			if r.API.HTTP == nil {
				r.API.HTTP = &expr.HTTPExpr{}
			}
			httpSvc := builder.buildHTTPService(mcpService)
			httpSvc.Root = r.API.HTTP
			if r.API.JSONRPC == nil {
				r.API.JSONRPC = &expr.JSONRPCExpr{}
			}

			// Remove original HTTP/JSON-RPC services for MCP-enabled service from the example
			{
				// HTTP
				if len(r.API.HTTP.Services) > 0 {
					filtered := make([]*expr.HTTPServiceExpr, 0, len(r.API.HTTP.Services))
					for _, hs := range r.API.HTTP.Services {
						if hs.ServiceExpr != nil && hs.ServiceExpr.Name == svc.Name {
							continue
						}
						filtered = append(filtered, hs)
					}
					r.API.HTTP.Services = filtered
				}
				// JSON-RPC
				if len(r.API.JSONRPC.Services) > 0 {
					filtered := make([]*expr.HTTPServiceExpr, 0, len(r.API.JSONRPC.Services))
					for _, js := range r.API.JSONRPC.Services {
						if js.ServiceExpr != nil && js.ServiceExpr.Name == svc.Name {
							continue
						}
						filtered = append(filtered, js)
					}
					r.API.JSONRPC.Services = filtered
				}
			}
			// Add to JSONRPC.HTTP services if not already present
			already := false
			for _, hs := range r.API.JSONRPC.Services {
				if hs.ServiceExpr != nil && hs.ServiceExpr.Name == httpSvc.ServiceExpr.Name {
					already = true
					break
				}
			}
			if !already {
				r.API.JSONRPC.Services = append(r.API.JSONRPC.Services, httpSvc)
			}
			// Remove original JSON-RPC service for this server so /rpc is handled by MCP only
			if len(r.API.JSONRPC.Services) > 0 {
				filtered := make([]*expr.HTTPServiceExpr, 0, len(r.API.JSONRPC.Services))
				for _, js := range r.API.JSONRPC.Services {
					if js.ServiceExpr != nil && js.ServiceExpr.Name == svc.Name {
						continue
					}
					filtered = append(filtered, js)
				}
				r.API.JSONRPC.Services = filtered
			}
			// Add MCP service once to JSONRPC services
			present := false
			for _, js := range r.API.JSONRPC.Services {
				if js.ServiceExpr != nil && js.ServiceExpr.Name == httpSvc.ServiceExpr.Name {
					present = true
					break
				}
			}
			if !present {
				r.API.JSONRPC.Services = append(r.API.JSONRPC.Services, httpSvc)
			}
			// Add mcp service once to top-level services
			if !serviceInList(r.Services, mcpService.Name) {
				r.Services = append(r.Services, mcpService)
			}
			// Replace the source service with its MCP transport service on servers
			// that originally exposed it. The generated MCP factory constructs the
			// source implementation itself, so retaining the source service here
			// produces an unused endpoint variable in prompt-only examples.
			for _, srv := range r.API.Servers {
				if !stringInList(srv.Services, svc.Name) {
					continue
				}
				services := make([]string, 0, len(srv.Services))
				for _, name := range srv.Services {
					if name != svc.Name && name != mcpService.Name {
						services = append(services, name)
					}
				}
				services = append(services, mcpService.Name)
				srv.Services = services
			}
		}
	}
	return nil
}

// ModifyExampleFiles patches example CLI wiring to target the MCP adapter client
// and replaces the default MCP stub factory to return the adapter-wrapped
// service. It avoids touching HTTP server signatures or example mains.
func ModifyExampleFiles(genpkg string, roots []eval.Root, files []*codegen.File) ([]*codegen.File, error) {
	r, ok := firstRootWithJSONRPC(roots)
	if !ok {
		return files, nil
	}

	mcpServices := collectMCPServices(r)
	if len(mcpServices) == 0 {
		return files, nil
	}

	// Ensure example stub returns the adapter-backed service instead of zero-value stub
	files, err := generateExampleAdapterStubs(genpkg, mcpServices, files)
	if err != nil {
		return nil, err
	}
	servers := make(example.ServersData)

	for _, svr := range r.API.Servers {
		dir := servers.Get(svr, r).Dir
		files, err = patchCLIForServer(dir, svr, mcpServices, files)
		if err != nil {
			return nil, err
		}
	}

	return files, nil
}

// firstRootWithJSONRPC returns the first root with JSON-RPC configured.
func firstRootWithJSONRPC(roots []eval.Root) (*expr.RootExpr, bool) {
	for _, root := range roots {
		r, ok := root.(*expr.RootExpr)
		if !ok || r.API == nil || r.API.JSONRPC == nil {
			continue
		}
		return r, true
	}
	return nil, false
}

// collectMCPServices returns services that have MCP configured in DSL.
func collectMCPServices(r *expr.RootExpr) []*expr.ServiceExpr {
	var svcs []*expr.ServiceExpr
	for _, sv := range r.Services {
		if mcpexpr.Root.HasMCP(sv) {
			svcs = append(svcs, sv)
		}
	}
	return svcs
}

// patchCLIForServer replaces the generated JSON-RPC CLI ownership sections
// with MCP adapter client endpoint wiring.
func patchCLIForServer(dir string, svr *expr.ServerExpr, mcpServices []*expr.ServiceExpr, files []*codegen.File) ([]*codegen.File, error) {
	svcMap := make(map[string]*expr.ServiceExpr, len(mcpServices))
	for _, svc := range mcpServices {
		svcMap[svc.Name] = svc
	}

	var targetSvcs []*expr.ServiceExpr
	for _, name := range svr.Services {
		if svc := svcMap[name]; svc != nil && len(svc.Methods) > 0 {
			targetSvcs = append(targetSvcs, svc)
		}
	}
	if len(targetSvcs) == 0 {
		return files, nil
	}

	cliPath := filepath.Join("cmd", dir+"-cli", "jsonrpc.go")
	cliFile, err := requireExampleFile(files, cliPath)
	if err != nil {
		return nil, err
	}
	header, err := requireExampleTemplateSection(cliFile, headerSection)
	if err != nil {
		return nil, err
	}
	if err := validateCLISectionOwnership(cliFile); err != nil {
		return nil, err
	}
	baseModule := deriveBaseModuleFromHeader(header)
	if baseModule == "" {
		return nil, fmt.Errorf("upstream example CLI import contract changed in %s: generated module import not found", filepath.ToSlash(cliFile.Path))
	}

	serviceData := buildCLIServiceData(targetSvcs, header, baseModule)
	if len(serviceData) == 0 {
		return nil, fmt.Errorf("upstream example CLI service contract changed in %s: no MCP methods found", filepath.ToSlash(cliFile.Path))
	}

	addHeaderImports(header,
		&codegen.ImportSpec{Path: "fmt"},
		&codegen.ImportSpec{Path: "os"},
		&codegen.ImportSpec{Path: "strings"},
	)

	sections := cliFile.AllSections()
	updated := make([]codegen.Section, 0, len(sections)-1)
	for _, section := range sections {
		switch section.SectionName() {
		case "cli-http-start":
			updated = append(updated, cliDoJSONRPCSection(serviceData))
		case "cli-http-end":
			continue
		default:
			updated = append(updated, section)
		}
	}
	cliFile.SetSections(updated)
	return files, nil
}

// generateExampleAdapterStubs replaces each generated MCP example stub at its
// stable path with an owned adapter factory section.
func generateExampleAdapterStubs(genpkg string, mcpServices []*expr.ServiceExpr, files []*codegen.File) ([]*codegen.File, error) {
	if len(mcpServices) == 0 {
		return files, nil
	}
	for _, svc := range mcpServices {
		svcGo := codegen.Goify(svc.Name, true)
		svcSnake := codegen.SnakeCase(svc.Name)
		stubPath := filepath.ToSlash("mcp_" + svcSnake + ".go")
		f, err := requireExampleFile(files, stubPath)
		if err != nil {
			return nil, err
		}
		header, err := requireExampleTemplateSection(f, headerSection)
		if err != nil {
			return nil, err
		}
		// Ensure import for MCP service package exists and capture its alias
		mcpAlias := ""
		if data, ok := header.Data.(map[string]any); ok {
			if imv, ok2 := data["Imports"]; ok2 {
				if specs, ok3 := imv.([]*codegen.ImportSpec); ok3 {
					for _, spec := range specs {
						if strings.HasSuffix(spec.Path, "/gen/mcp_"+svcSnake) {
							mcpAlias = spec.Name
							break
						}
					}
				}
			}
		}
		if mcpAlias == "" {
			mcpImportPath := ""
			if genpkg != "" {
				mcpImportPath = strings.TrimSuffix(genpkg, "/") + "/mcp_" + svcSnake
			} else if base := deriveBaseModuleFromFiles(files); base != "" {
				mcpImportPath = base + "/gen/mcp_" + svcSnake
			}
			if mcpImportPath != "" {
				mcpAlias = codegen.Goify("mcp_"+svcSnake, false)
				addHeaderImports(header, &codegen.ImportSpec{Path: mcpImportPath, Name: mcpAlias})
			}
		}
		if mcpAlias == "" {
			return nil, fmt.Errorf("upstream example stub import contract changed in %s: MCP service import not found", stubPath)
		}
		// Determine whether prompts are enabled to decide constructor arity
		hasPrompts := false
		if m := mcpexpr.Root.GetMCP(svc); m != nil {
			hasPrompts = m.Capabilities != nil && m.Capabilities.EnablePrompts
		}
		f.SetSections([]codegen.Section{header, exampleAdapterStubSection(svcGo, mcpAlias, hasPrompts)})
	}
	return files, nil
}

func requireExampleFile(files []*codegen.File, path string) (*codegen.File, error) {
	var matched *codegen.File
	count := 0
	for _, file := range files {
		if file == nil || filepath.ToSlash(file.Path) != filepath.ToSlash(path) {
			continue
		}
		matched = file
		count++
	}
	if count != 1 {
		return nil, fmt.Errorf("upstream example file contract changed: expected one %q file, found %d", filepath.ToSlash(path), count)
	}
	return matched, nil
}

func requireExampleTemplateSection(file *codegen.File, name string) (*codegen.SectionTemplate, error) {
	sections := file.Section(name)
	if len(sections) != 1 {
		return nil, fmt.Errorf(
			"upstream example section contract changed in %s: expected one %q section, found %d",
			filepath.ToSlash(file.Path), name, len(sections),
		)
	}
	template, ok := sections[0].(*codegen.SectionTemplate)
	if !ok {
		return nil, fmt.Errorf(
			"upstream example section contract changed in %s: %q is not template-backed",
			filepath.ToSlash(file.Path), name,
		)
	}
	return template, nil
}

func validateCLISectionOwnership(file *codegen.File) error {
	for _, name := range []string{"cli-http-start", "cli-http-end"} {
		count := len(file.Section(name))
		if count != 1 {
			return fmt.Errorf(
				"upstream example CLI section contract changed in %s: expected one %q section, found %d",
				filepath.ToSlash(file.Path), name, count,
			)
		}
	}
	return nil
}

func exampleAdapterStubSection(serviceGo, mcpAlias string, hasPrompts bool) codegen.Section {
	return codegen.NewJenniferSection(exampleMCPStubSection, func(stmt *jen.Statement) {
		stmt.Comment("Example MCP stub: ensure NewMcp" + serviceGo + " returns the adapter-wrapped service.").Line()
		stmt.Func().Id("NewMcp" + serviceGo).Params().Id(mcpAlias).Dot("Service").BlockFunc(func(g *jen.Group) {
			args := []jen.Code{jen.Id("New" + serviceGo).Call(), jen.Nil()}
			if hasPrompts {
				args = append(args, jen.Nil())
			}
			g.Return(jen.Id(mcpAlias).Dot("NewMCPAdapter").Call(args...))
		})
	})
}

// deriveBaseModuleFromFiles attempts to locate the module import prefix by inspecting
// CLI files that import gen/jsonrpc/cli/. Returns empty string if not found.
func deriveBaseModuleFromFiles(files []*codegen.File) string {
	for _, f := range files {
		p := filepath.ToSlash(f.Path)
		if !strings.Contains(p, "/cmd/") || !strings.HasSuffix(p, "/jsonrpc.go") {
			continue
		}
		header := findSection(f, headerSection)
		if header == nil {
			continue
		}
		if base := deriveBaseModuleFromHeader(header); base != "" {
			return base
		}
	}
	return ""
}

// findSection returns the first section with the given name in file f.
func findSection(f *codegen.File, name string) *codegen.SectionTemplate {
	for _, sec := range f.AllSections() {
		s, ok := sec.(*codegen.SectionTemplate)
		if ok && s.Name == name {
			return s
		}
	}
	return nil
}

// deriveBaseModuleFromHeader inspects the header imports to find the module path
// prefix used by the generated example code (by locating the JSON-RPC CLI import).
func deriveBaseModuleFromHeader(header *codegen.SectionTemplate) string {
	if header == nil || header.Data == nil {
		return ""
	}
	var specs []*codegen.ImportSpec
	if data := codegen.HeaderSectionData(header); data != nil {
		specs = data.Imports
	} else {
		raw, ok := header.Data.(map[string]any)
		if !ok {
			return ""
		}
		imv, ok := raw["Imports"]
		if !ok {
			return ""
		}
		var okSpecs bool
		specs, okSpecs = imv.([]*codegen.ImportSpec)
		if !okSpecs {
			return ""
		}
	}
	for _, spec := range specs {
		idx := strings.Index(spec.Path, "/gen/jsonrpc/cli/")
		if idx >= 0 {
			return spec.Path[:idx]
		}
	}
	return ""
}

func serviceInList(list []*expr.ServiceExpr, name string) bool {
	for _, s := range list {
		if s.Name == name {
			return true
		}
	}
	return false
}

func stringInList(list []string, name string) bool {
	for _, s := range list {
		if s == name {
			return true
		}
	}
	return false
}

func buildCLIServiceData(
	services []*expr.ServiceExpr,
	header *codegen.SectionTemplate,
	baseModule string,
) []cliServiceTemplateData {
	data := make([]cliServiceTemplateData, 0, len(services))
	for _, svc := range services {
		if len(svc.Methods) == 0 {
			continue
		}
		svcSnake := codegen.SnakeCase(svc.Name)
		alias := codegen.Goify("mcp_"+svcSnake, false) + "adapter"
		path := baseModule + "/gen/mcp_" + svcSnake + "/adapter/client"
		addHeaderImports(header, &codegen.ImportSpec{Path: path, Name: alias})

		methods := make([]cliMethodTemplateData, 0, len(svc.Methods))
		for _, m := range svc.Methods {
			methods = append(methods, cliMethodTemplateData{
				Command:  methodCommandName(m.Name),
				Endpoint: codegen.Goify(m.Name, true),
			})
		}
		data = append(data, cliServiceTemplateData{
			Name:    svc.Name,
			Alias:   alias,
			Methods: methods,
		})
	}
	return data
}

func addHeaderImports(header *codegen.SectionTemplate, specs ...*codegen.ImportSpec) {
	if header == nil || len(specs) == 0 {
		return
	}
	if data := codegen.HeaderSectionData(header); data != nil {
		data.Imports = append(data.Imports, specs...)
		return
	}
	raw, ok := header.Data.(map[string]any)
	if !ok {
		return
	}
	if existing, ok := raw["Imports"].([]*codegen.ImportSpec); ok {
		raw["Imports"] = append(existing, specs...)
	}
}

func cliDoJSONRPCSection(services []cliServiceTemplateData) codegen.Section {
	return codegen.NewJenniferSection("cli-dojsonrpc", func(stmt *jen.Statement) {
		emitCLIDoJSONRPC(stmt, services)
	})
}

func methodCommandName(name string) string {
	return strings.ReplaceAll(codegen.SnakeCase(name), "_", "-")
}

func emitCLIDoJSONRPC(stmt *jen.Statement, services []cliServiceTemplateData) {
	stmt.Func().Id("doJSONRPC").
		Params(
			jen.Id("scheme").String(),
			jen.Id("host").String(),
			jen.Id("timeout").Int(),
			jen.Id("debug").Bool(),
		).
		Params(jen.Id("loom").Dot("Endpoint"), jen.Any(), jen.Error()).
		BlockFunc(func(g *jen.Group) {
			g.Var().Id("doer").Id("goahttp").Dot("Doer")
			g.Block(
				jen.Id("doer").Op("=").Op("&").Qual("net/http", "Client").Values(jen.Dict{
					jen.Id("Timeout"): jen.Qual("time", "Duration").Call(jen.Id("timeout")).Op("*").Qual("time", "Second"),
				}),
				jen.If(jen.Id("debug")).Block(
					jen.Id("doer").Op("=").Id("goahttp").Dot("NewDebugDoer").Call(jen.Id("doer")),
				),
			)
			g.Line()
			g.List(jen.Id("endpoint"), jen.Id("payload"), jen.Id("err")).Op(":=").Id("cli").Dot("ParseEndpoint").Call(
				jen.Id("scheme"),
				jen.Id("host"),
				jen.Id("doer"),
				jen.Id("goahttp").Dot("RequestEncoder"),
				jen.Id("goahttp").Dot("ResponseDecoder"),
				jen.Id("debug"),
			)
			g.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Nil(), jen.Id("err")),
			)
			g.Line()
			g.Var().Id("nonflags").Index().String()
			g.For(jen.Id("i").Op(":=").Lit(1), jen.Id("i").Op("<").Len(jen.Id("os").Dot("Args")), jen.Id("i").Op("++")).Block(
				jen.Id("a").Op(":=").Id("os").Dot("Args").Index(jen.Id("i")),
				jen.If(jen.Qual("strings", "HasPrefix").Call(jen.Id("a"), jen.Lit("-"))).Block(
					jen.If(jen.Op("!").Qual("strings", "Contains").Call(jen.Id("a"), jen.Lit("=")).Op("&&").Id("i").Op("+").Lit(1).Op("<").Len(jen.Id("os").Dot("Args"))).Block(
						jen.Id("i").Op("++"),
					),
					jen.Continue(),
				),
				jen.Id("nonflags").Op("=").Append(jen.Id("nonflags"), jen.Id("a")),
			)
			g.If(jen.Len(jen.Id("nonflags")).Op("<").Lit(2)).Block(
				jen.Return(jen.Nil(), jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("not enough arguments"))),
			)
			g.Line()
			g.Id("service").Op(":=").Id("nonflags").Index(jen.Lit(0))
			g.Id("subcmd").Op(":=").Id("nonflags").Index(jen.Lit(1))
			g.Line()
			g.Switch(jen.Id("service")).BlockFunc(func(sw *jen.Group) {
				for _, svc := range services {
					sw.Case(jen.Lit(svc.Name)).BlockFunc(func(caseg *jen.Group) {
						caseg.Id("e").Op(":=").Id(svc.Alias).Dot("NewEndpoints").Call(
							jen.Id("scheme"),
							jen.Id("host"),
							jen.Id("doer"),
							jen.Id("goahttp").Dot("RequestEncoder"),
							jen.Id("goahttp").Dot("ResponseDecoder"),
							jen.Id("debug"),
						)
						caseg.Switch(jen.Id("subcmd")).BlockFunc(func(subsw *jen.Group) {
							for _, method := range svc.Methods {
								subsw.Case(jen.Lit(method.Command)).Block(
									jen.Return(jen.Id("e").Dot(method.Endpoint), jen.Id("payload"), jen.Nil()),
								)
							}
						})
						caseg.Return(jen.Id("endpoint"), jen.Id("payload"), jen.Nil())
					})
				}
			})
			g.Line()
			g.Return(jen.Id("endpoint"), jen.Id("payload"), jen.Nil())
		})
}
