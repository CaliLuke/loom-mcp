package codegen

import (
	"path/filepath"
	"sort"

	"github.com/CaliLuke/loom-mcp/v2/runtime/mcp/sdkbridge"
	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/expr"
	"github.com/dave/jennifer/jen"
)

func buildMCPSDKServerFile(genpkg string, svc *expr.ServiceExpr, data *AdapterData, svcName, pkgName string) *codegen.File {
	sdkServerImports := []*codegen.ImportSpec{
		{Path: "context"},
		{Path: "encoding/base64"},
		{Name: "json", Path: "encoding/json/v2"},
		{Name: "jsontext", Path: "encoding/json/jsontext"},
		{Path: "errors"},
		{Path: "fmt"},
		{Path: "net/http"},
		{Path: "slices"},
		{Path: genpkg + "/" + svcName, Name: svcName},
		{Path: "github.com/modelcontextprotocol/go-sdk/mcp", Name: "mcpsdk"},
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/mcp/sdkbridge", Name: "sdkbridge"},
		{Path: "github.com/CaliLuke/loom/http", Name: "loomhttp"},
		{Path: "github.com/CaliLuke/loom/observability/transport"},
	}
	projectedImports := make(map[string]string)
	for _, tool := range data.Tools {
		if tool == nil || tool.Projected == nil {
			continue
		}
		projectedImports[tool.Projected.SpecsPackageName] = tool.Projected.SpecsImportPath
	}
	projectedNames := make([]string, 0, len(projectedImports))
	for name := range projectedImports {
		projectedNames = append(projectedNames, name)
	}
	sort.Strings(projectedNames)
	for _, name := range projectedNames {
		sdkServerImports = append(sdkServerImports, &codegen.ImportSpec{Path: projectedImports[name], Name: name})
	}
	if hasWatchableResourceTemplates(data) {
		sdkServerImports = append(sdkServerImports, &codegen.ImportSpec{Path: "github.com/yosida95/uritemplate/v3", Name: "uritemplate"})
	}
	if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
		sdkServerImports = append(sdkServerImports, &codegen.ImportSpec{Path: "strings"})
	}
	if len(data.SkillDirectories) > 0 {
		sdkServerImports = append(sdkServerImports, &codegen.ImportSpec{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/mcp/skills", Name: "mcpskills"})
	}
	sections := []codegen.Section{
		codegen.Header("SDK-backed MCP server for "+svc.Name+" service", pkgName, sdkServerImports),
		sdkServerTypesSection(data),
		sdkServerConstructorSection(data),
		sdkServerRegistrationSection(data),
		sdkServerHandlerSection(data),
		sdkServerConversionSection(data),
	}
	return &codegen.File{Path: filepath.Join(codegen.Gendir, "mcp_"+svcName, "sdk_server.go"), Sections: sections}
}

func sdkServerTypesSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-sdk-server-types", func(stmt *jen.Statement) {
		stmt.Comment("SDKServer is an official SDK-backed MCP streamable HTTP server.").Line()
		stmt.Type().Id("SDKServer").Struct(
			jen.Id("Handler").Qual("net/http", "Handler"),
			jen.Id("Adapter").Op("*").Id("MCPAdapter"),
			jen.Id("Server").Op("*").Id("mcpsdk").Dot("Server"),
			jen.Id("bridge").Op("*").Id("sdkbridge").Dot("Server"),
		)
		stmt.Line()
		stmt.Comment("SDKServerOptions configures the generated service binding and shared SDK bridge.").Line()
		stmt.Type().Id("SDKServerOptions").StructFunc(func(g *jen.Group) {
			g.Id("Adapter").Op("*").Id("MCPAdapterOptions")
			g.Id("RequestContext").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Qual("net/http", "Request")).Qual("context", "Context")
			g.Comment("RequestStateKey encrypts and authenticates multi-round-trip request state. It must contain exactly 32 bytes when a flow emits or consumes requestState.")
			g.Id("RequestStateKey").Index().Byte()
			g.Id("TransportObserver").Qual("github.com/CaliLuke/loom/observability/transport", "Observer")
			g.Id("RuntimeCORS").Op("*").Id("loomhttp").Dot("RuntimeCORSPolicy")
			if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
				g.Id("PromptProvider").Id("PromptProvider")
			}
			g.Id("Server").Op("*").Id("mcpsdk").Dot("ServerOptions")
			g.Id("OriginProtection").Op("*").Id("sdkbridge").Dot("OriginProtection")
			g.Id("StreamableHTTP").Op("*").Id("sdkbridge").Dot("StreamableHTTPOptions")
		})
		stmt.Line()
	})
}

func sdkServerConstructorSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-sdk-server-constructor", func(stmt *jen.Statement) {
		stmt.Comment("NewSDKServer constructs the generated service adapter and shared official SDK bridge.").Line()
		stmt.Func().Id("NewSDKServer").Params(
			jen.Id("service").Id(data.Package).Dot("Service"),
			jen.Id("opts").Op("*").Id("SDKServerOptions"),
		).Params(jen.Op("*").Id("SDKServer"), jen.Error()).BlockFunc(func(g *jen.Group) {
			g.Var().Id("adapterOpts").Op("*").Id("MCPAdapterOptions")
			g.Var().Id("requestContext").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Qual("net/http", "Request")).Qual("context", "Context")
			g.Var().Id("requestStateKey").Index().Byte()
			g.Var().Id("bridgeOptions").Id("sdkbridge").Dot("Options")
			if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
				g.Var().Id("promptProvider").Id("PromptProvider")
			}
			g.If(jen.Id("opts").Op("!=").Nil()).BlockFunc(func(ifg *jen.Group) {
				ifg.Id("adapterOpts").Op("=").Id("opts").Dot("Adapter")
				ifg.Id("requestContext").Op("=").Id("opts").Dot("RequestContext")
				ifg.Id("requestStateKey").Op("=").Id("opts").Dot("RequestStateKey")
				if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
					ifg.Id("promptProvider").Op("=").Id("opts").Dot("PromptProvider")
				}
				ifg.Id("bridgeOptions").Op("=").Id("sdkbridge").Dot("Options").Values(jen.Dict{
					jen.Id("RequestContext"):    jen.Id("requestContext"),
					jen.Id("TransportObserver"): jen.Id("opts").Dot("TransportObserver"),
					jen.Id("RuntimeCORS"):       jen.Id("opts").Dot("RuntimeCORS"),
					jen.Id("Server"):            jen.Id("opts").Dot("Server"),
					jen.Id("OriginProtection"):  jen.Id("opts").Dot("OriginProtection"),
					jen.Id("StreamableHTTP"):    jen.Id("opts").Dot("StreamableHTTP"),
				})
			})
			if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
				g.Id("adapter").Op(":=").Id("NewMCPAdapter").Call(jen.Id("service"), jen.Id("promptProvider"), jen.Id("adapterOpts"))
			} else {
				g.Id("adapter").Op(":=").Id("NewMCPAdapter").Call(jen.Id("service"), jen.Id("adapterOpts"))
			}
			g.Id("adapter").Dot("requestStateKey").Op("=").Qual("slices", "Clone").Call(jen.Id("requestStateKey"))
			config := jen.Dict{
				jen.Id("CompatibilityVersion"): jen.Lit(sdkbridge.CompatibilityVersion),
				jen.Id("Implementation"):       jen.Id("mcpsdk").Dot("Implementation").Values(sdkImplementationDict(data)),
				jen.Id("Tools"):                jen.Func().Params().Params(jen.Index().Id("sdkbridge").Dot("ToolBinding"), jen.Error()).Block(jen.Return(jen.Id("sdkToolBindings").Call(jen.Id("adapter")))),
				jen.Id("Resources"):            jen.Func().Params().Params(jen.Index().Id("sdkbridge").Dot("ResourceBinding"), jen.Error()).Block(jen.Return(jen.Id("sdkResourceBindings").Call(jen.Id("adapter")))),
				jen.Id("Prompts"):              jen.Func().Params().Params(jen.Index().Id("sdkbridge").Dot("PromptBinding"), jen.Error()).Block(jen.Return(jen.Id("sdkPromptBindings").Call(jen.Id("adapter")))),
				jen.Id("Sessions"):             jen.Id("adapter").Dot("sessions"),
				jen.Id("Options"):              jen.Id("bridgeOptions"),
			}
			if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
				config[jen.Id("CompletionHandler")] = jen.Id("adapter").Dot("sdkCompletionHandler").Call()
			}
			if data.HasWatchableResources {
				config[jen.Id("WatchableResource")] = sdkWatchableResourceFunc(data)
			}
			g.List(jen.Id("runtimeBridge"), jen.Id("err")).Op(":=").Id("sdkbridge").Dot("NewServer").Call(jen.Id("sdkbridge").Dot("Config").Values(config))
			g.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err")))
			g.Return(jen.Op("&").Id("SDKServer").Values(jen.Dict{
				jen.Id("Handler"): jen.Id("runtimeBridge").Dot("Handler"),
				jen.Id("Adapter"): jen.Id("adapter"),
				jen.Id("Server"):  jen.Id("runtimeBridge").Dot("SDK"),
				jen.Id("bridge"):  jen.Id("runtimeBridge"),
			}), jen.Nil())
		})
		stmt.Line()
		stmt.Comment("ResourceUpdated notifies subscribed clients that a designed watchable resource changed.").Line()
		stmt.Func().Params(jen.Id("s").Op("*").Id("SDKServer")).Id("ResourceUpdated").Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("uri").String()).Error().Block(
			jen.If(jen.Id("s").Op("==").Nil().Op("||").Id("s").Dot("bridge").Op("==").Nil()).Block(jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("MCP SDK server is not initialized")))),
			jen.Return(jen.Id("s").Dot("bridge").Dot("ResourceUpdated").Call(jen.Id("ctx"), jen.Id("uri"))),
		)
		stmt.Line()
	})
}

const sdkWatchableResourceMatchersName = "sdkWatchableResourceMatchers"

func hasWatchableResourceTemplates(data *AdapterData) bool {
	for _, resource := range data.Resources {
		if resource.Watchable && resource.HasPayload {
			return true
		}
	}
	return false
}

func sdkWatchableResourceFunc(data *AdapterData) jen.Code {
	return jen.Func().Params(jen.Id("uri").String()).Bool().BlockFunc(func(g *jen.Group) {
		g.Switch(jen.Id("uri")).BlockFunc(func(cases *jen.Group) {
			for _, resource := range data.Resources {
				if resource.Watchable && !resource.HasPayload {
					cases.Case(jen.Lit(resource.URI)).Block(jen.Return(jen.True()))
				}
			}
		})
		if hasWatchableResourceTemplates(data) {
			g.For(jen.List(jen.Id("_"), jen.Id("matcher")).Op(":=").Range().Id(sdkWatchableResourceMatchersName)).Block(
				jen.If(jen.Id("matcher").Dot("Match").Call(jen.Id("uri"))).Block(jen.Return(jen.True())),
			)
		}
		g.Return(jen.False())
	})
}

func emitSDKWatchableResourceMatchers(stmt *jen.Statement, data *AdapterData) {
	if !hasWatchableResourceTemplates(data) {
		return
	}
	stmt.Var().Id(sdkWatchableResourceMatchersName).Op("=").Index().Id("sdkbridge").Dot("ResourceURIMatcher").ValuesFunc(func(values *jen.Group) {
		for _, resource := range data.Resources {
			if resource.Watchable && resource.HasPayload {
				values.Values(jen.Dict{
					jen.Id("Pattern"):     jen.Id("uritemplate").Dot("MustNew").Call(jen.Lit(resourceQueryURITemplate(resource.URI, resource.QueryFields))).Dot("Regexp").Call(),
					jen.Id("QueryFields"): resourceQueryFieldsValue(resource, "sdkbridge", "ResourceQueryField"),
					jen.Id("QuerySchema"): jen.Lit(resource.QuerySchema),
				})
			}
		}
	})
	stmt.Line()
}

func sdkImplementationDict(data *AdapterData) jen.Dict {
	dict := jen.Dict{
		jen.Id("Name"):    jen.Lit(data.MCPName),
		jen.Id("Version"): jen.Lit(data.MCPVersion),
	}
	if data.WebsiteURL != "" {
		dict[jen.Id("WebsiteURL")] = jen.Lit(data.WebsiteURL)
	}
	if icons := sdkIconSliceValue(data.Icons); icons != nil {
		dict[jen.Id("Icons")] = icons
	}
	return dict
}

func sdkServerRegistrationSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-sdk-server-registration", func(stmt *jen.Statement) {
		emitSDKWatchableResourceMatchers(stmt, data)
		emitSDKToolBindings(stmt, data)
		emitSDKResourceBindings(stmt, data)
		emitSDKPromptBindings(stmt, data)
		stmt.Func().Id("sdkToolAnnotations").
			Params(jen.Id("raw").Any()).
			Params(jen.Op("*").Id("mcpsdk").Dot("ToolAnnotations"), jen.Error()).
			Block(
				jen.If(jen.Id("raw").Op("==").Nil()).Block(
					jen.Return(jen.Nil(), jen.Nil()),
				),
				jen.List(jen.Id("data"), jen.Id("err")).Op(":=").Id("json").Dot("Marshal").Call(jen.Id("raw")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
				jen.Var().Id("annotations").Id("mcpsdk").Dot("ToolAnnotations"),
				jen.If(jen.Id("err").Op(":=").Id("json").Dot("Unmarshal").Call(jen.Id("data"), jen.Op("&").Id("annotations")), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				),
				jen.Return(jen.Op("&").Id("annotations"), jen.Nil()),
			)
		stmt.Line()
		stmt.Func().Id("sdkToolInputSchema").
			Params(jen.Id("raw").String()).
			Any().
			Block(
				jen.If(jen.Id("raw").Op("==").Lit("")).Block(
					jen.Return(jen.Id("jsontext").Dot("Value").Call(jen.Lit(`{"type":"object"}`))),
				),
				jen.Return(jen.Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Id("raw")))),
			)
		stmt.Line()
		stmt.Func().Id("sdkToolFromToolInfo").
			Params(jen.Id("tool").Op("*").Id("ToolInfo")).
			Params(jen.Op("*").Id("mcpsdk").Dot("Tool"), jen.Error()).
			Block(
				jen.If(jen.Id("tool").Op("==").Nil()).Block(
					jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("nil MCP tool info"))),
				),
				jen.List(jen.Id("annotations"), jen.Id("err")).Op(":=").Id("sdkToolAnnotations").Call(jen.Id("tool").Dot("Annotations")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				),
				jen.List(jen.Id("meta"), jen.Id("err")).Op(":=").Id("sdkMeta").Call(jen.Id("tool").Dot("Meta")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				),
				jen.Id("sdkTool").Op(":=").Op("&").Id("mcpsdk").Dot("Tool").Values(jen.Dict{
					jen.Id("Name"):         jen.Id("tool").Dot("Name"),
					jen.Id("Title"):        jen.Id("derefString").Call(jen.Id("tool").Dot("Title")),
					jen.Id("Description"):  jen.Id("derefString").Call(jen.Id("tool").Dot("Description")),
					jen.Id("InputSchema"):  jen.Id("tool").Dot("InputSchema"),
					jen.Id("OutputSchema"): jen.Id("tool").Dot("OutputSchema"),
					jen.Id("Annotations"):  jen.Id("annotations"),
					jen.Id("Meta"):         jen.Id("meta"),
				}),
				jen.If(jen.Len(jen.Id("tool").Dot("Icons")).Op(">").Lit(0)).Block(
					jen.Id("sdkTool").Dot("Icons").Op("=").Make(jen.Index().Id("mcpsdk").Dot("Icon"), jen.Lit(0), jen.Len(jen.Id("tool").Dot("Icons"))),
					jen.For(jen.List(jen.Id("_"), jen.Id("icon")).Op(":=").Range().Id("tool").Dot("Icons")).Block(
						jen.Id("sdkIcon").Op(":=").Id("mcpsdk").Dot("Icon").Values(jen.Dict{
							jen.Id("Source"):   jen.Id("icon").Dot("Src"),
							jen.Id("MIMEType"): jen.Id("derefString").Call(jen.Id("icon").Dot("MimeType")),
							jen.Id("Sizes"):    jen.Id("icon").Dot("Sizes"),
							jen.Id("Theme"):    jen.Id("mcpsdk").Dot("IconTheme").Call(jen.Id("derefString").Call(jen.Id("icon").Dot("Theme"))),
						}),
						jen.Id("sdkTool").Dot("Icons").Op("=").Append(jen.Id("sdkTool").Dot("Icons"), jen.Id("sdkIcon")),
					),
				),
				jen.Return(jen.Id("sdkTool"), jen.Nil()),
			)
		stmt.Line()
	})
}

func emitSDKToolBindings(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Id("sdkToolBindings").Params(
		jen.Id("adapter").Op("*").Id("MCPAdapter"),
	).Params(jen.Index().Id("sdkbridge").Dot("ToolBinding"), jen.Error()).BlockFunc(func(g *jen.Group) {
		g.Id("handler").Op(":=").Id("adapter").Dot("sdkToolHandler").Call()
		g.If(jen.Id("adapter").Dot("toolSearchEnabled").Call()).Block(
			jen.Id("tools").Op(":=").Id("adapter").Dot("toolSearchSyntheticTools").Call(),
			jen.Id("tools").Op("=").Append(jen.Id("tools"), jen.Id("adapter").Dot("visibleToolCatalog").Call(jen.Id("adapter").Dot("generatedToolCatalog").Call()).Op("...")),
			jen.Id("bindings").Op(":=").Make(jen.Index().Id("sdkbridge").Dot("ToolBinding"), jen.Lit(0), jen.Len(jen.Id("tools"))),
			jen.For(jen.List(jen.Id("_"), jen.Id("tool")).Op(":=").Range().Id("tools")).Block(
				jen.List(jen.Id("sdkTool"), jen.Id("err")).Op(":=").Id("sdkToolFromToolInfo").Call(jen.Id("tool")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
				jen.Id("bindings").Op("=").Append(jen.Id("bindings"), jen.Id("sdkbridge").Dot("ToolBinding").Values(jen.Dict{jen.Id("Tool"): jen.Id("sdkTool"), jen.Id("Handler"): jen.Id("handler")})),
			),
			jen.Return(jen.Id("bindings"), jen.Nil()),
		)
		g.Id("bindings").Op(":=").Make(jen.Index().Id("sdkbridge").Dot("ToolBinding"), jen.Lit(0), jen.Lit(len(data.Tools)))
		for _, tool := range data.Tools {
			if tool.AnnotationsJSON != "" {
				name := "annotations" + codegen.Goify(tool.Name, true)
				g.List(jen.Id(name), jen.Id("err")).Op(":=").Id("sdkToolAnnotations").Call(jen.Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Lit(tool.AnnotationsJSON))))
				g.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("tool %q annotations: %w"), jen.Lit(tool.Name), jen.Id("err"))))
			}
			metaName := "meta" + codegen.Goify(tool.Name, true)
			if tool.MetaJSON != "" {
				g.List(jen.Id(metaName), jen.Id("err")).Op(":=").Id("sdkMeta").Call(jen.Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Lit(tool.MetaJSON))))
				g.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("tool %q metadata: %w"), jen.Lit(tool.Name), jen.Id("err"))))
			}
			dict := jen.Dict{jen.Id("Name"): jen.Lit(tool.Name), jen.Id("Title"): jen.Lit(tool.Title), jen.Id("Description"): jen.Lit(tool.Description), jen.Id("InputSchema"): sdkToolSchemaValue(tool, true)}
			if outputSchema := sdkToolSchemaValue(tool, false); outputSchema != nil {
				dict[jen.Id("OutputSchema")] = outputSchema
			}
			if tool.MetaJSON != "" {
				dict[jen.Id("Meta")] = jen.Id(metaName)
			}
			if icons := sdkIconSliceValue(tool.Icons); icons != nil {
				dict[jen.Id("Icons")] = icons
			}
			if tool.AnnotationsJSON != "" {
				dict[jen.Id("Annotations")] = jen.Id("annotations" + codegen.Goify(tool.Name, true))
			}
			g.Id("bindings").Op("=").Append(jen.Id("bindings"), jen.Id("sdkbridge").Dot("ToolBinding").Values(jen.Dict{
				jen.Id("Tool"):    jen.Op("&").Id("mcpsdk").Dot("Tool").Values(dict),
				jen.Id("Handler"): jen.Id("handler"),
			}))
		}
		g.Return(jen.Id("bindings"), jen.Nil())
	})
	stmt.Line()
}

func sdkToolSchemaValue(tool *ToolAdapter, input bool) jen.Code {
	if tool.Projected != nil {
		if input {
			return jen.Id("jsontext").Dot("Value").Call(
				jen.Id(tool.Projected.SpecsPackageName).Dot(tool.Projected.SpecName).Dot("Payload").Dot("Schema"),
			)
		}
		if !input && tool.Projected.HasResult {
			return jen.Id("jsontext").Dot("Value").Call(
				jen.Id(tool.Projected.SpecsPackageName).Dot(tool.Projected.SpecName).Dot("Result").Dot("Schema"),
			)
		}
	}
	if input {
		return jen.Id("sdkToolInputSchema").Call(jen.Lit(tool.InputSchema))
	}
	if tool.OutputSchema == "" {
		return nil
	}
	return jen.Id("sdkToolInputSchema").Call(jen.Lit(tool.OutputSchema))
}

func emitSDKResourceBindings(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Id("sdkResourceBindings").Params(
		jen.Id("adapter").Op("*").Id("MCPAdapter"),
	).Params(jen.Index().Id("sdkbridge").Dot("ResourceBinding"), jen.Error()).BlockFunc(func(g *jen.Group) {
		if len(data.Resources) == 0 && len(data.SkillDirectories) == 0 {
			g.Return(jen.Nil(), jen.Nil())
			return
		}
		g.Id("handler").Op(":=").Id("adapter").Dot("sdkResourceHandler").Call()
		g.Id("bindings").Op(":=").Make(jen.Index().Id("sdkbridge").Dot("ResourceBinding"), jen.Lit(0), jen.Lit(len(data.Resources)))
		for _, resource := range data.Resources {
			descriptor := jen.Dict{
				jen.Id("Name"):        jen.Lit(resource.Name),
				jen.Id("Description"): jen.Lit(resource.Description),
				jen.Id("MIMEType"):    jen.Lit(resource.MimeType),
			}
			if icons := sdkIconSliceValue(resource.Icons); icons != nil {
				descriptor[jen.Id("Icons")] = icons
			}
			binding := jen.Dict{jen.Id("Handler"): jen.Id("handler")}
			if resource.HasPayload {
				descriptor[jen.Id("URITemplate")] = jen.Lit(resourceQueryURITemplate(resource.URI, resource.QueryFields))
				binding[jen.Id("Template")] = jen.Op("&").Id("mcpsdk").Dot("ResourceTemplate").Values(descriptor)
			} else {
				descriptor[jen.Id("URI")] = jen.Lit(resource.URI)
				binding[jen.Id("Resource")] = jen.Op("&").Id("mcpsdk").Dot("Resource").Values(descriptor)
			}
			g.Id("bindings").Op("=").Append(jen.Id("bindings"), jen.Id("sdkbridge").Dot("ResourceBinding").Values(binding))
		}
		if len(data.SkillDirectories) > 0 {
			g.List(jen.Id("skillResources"), jen.Id("err")).Op(":=").Id("mcpskills").Dot("List").Call(jen.Qual("context", "Background").Call(), jen.Id("skillSources").Call())
			g.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err")))
			g.For(jen.List(jen.Id("_"), jen.Id("resource")).Op(":=").Range().Id("skillResources")).Block(
				jen.List(jen.Id("meta"), jen.Id("err")).Op(":=").Id("sdkMeta").Call(jen.Id("mcpskills").Dot("MetadataMeta").Call(jen.Id("resource").Dot("Metadata"))),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
				jen.Id("bindings").Op("=").Append(jen.Id("bindings"), jen.Id("sdkbridge").Dot("ResourceBinding").Values(jen.Dict{
					jen.Id("Resource"): jen.Op("&").Id("mcpsdk").Dot("Resource").Values(jen.Dict{jen.Id("Name"): jen.Id("resource").Dot("Name"), jen.Id("URI"): jen.Id("resource").Dot("URI"), jen.Id("Description"): jen.Id("resource").Dot("Description"), jen.Id("MIMEType"): jen.Id("resource").Dot("MimeType"), jen.Id("Meta"): jen.Id("meta")}),
					jen.Id("Handler"):  jen.Id("handler"),
				})),
			)
		}
		g.Return(jen.Id("bindings"), jen.Nil())
	})
	stmt.Line()
}

func emitSDKPromptBindings(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Id("sdkPromptBindings").Params(
		jen.Id("adapter").Op("*").Id("MCPAdapter"),
	).Params(jen.Index().Id("sdkbridge").Dot("PromptBinding"), jen.Error()).BlockFunc(func(g *jen.Group) {
		if len(data.StaticPrompts) == 0 && len(data.DynamicPrompts) == 0 {
			g.Return(jen.Nil(), jen.Nil())
			return
		}
		g.Id("handler").Op(":=").Id("adapter").Dot("sdkPromptHandler").Call()
		g.Id("bindings").Op(":=").Make(jen.Index().Id("sdkbridge").Dot("PromptBinding"), jen.Lit(0), jen.Lit(len(data.StaticPrompts)+len(data.DynamicPrompts)))
		emitPrompt := func(name, description string, args []PromptArg, icons []*IconData) {
			dict := jen.Dict{jen.Id("Name"): jen.Lit(name), jen.Id("Description"): jen.Lit(description)}
			if len(args) > 0 {
				dict[jen.Id("Arguments")] = sdkPromptArgumentsValue(args)
			}
			if iconValue := sdkIconSliceValue(icons); iconValue != nil {
				dict[jen.Id("Icons")] = iconValue
			}
			g.Id("bindings").Op("=").Append(jen.Id("bindings"), jen.Id("sdkbridge").Dot("PromptBinding").Values(jen.Dict{
				jen.Id("Prompt"):  jen.Op("&").Id("mcpsdk").Dot("Prompt").Values(dict),
				jen.Id("Handler"): jen.Id("handler"),
			}))
		}
		for _, prompt := range data.StaticPrompts {
			emitPrompt(prompt.Name, prompt.Description, nil, prompt.Icons)
		}
		for _, prompt := range data.DynamicPrompts {
			emitPrompt(prompt.Name, prompt.Description, prompt.Arguments, prompt.Icons)
		}
		g.Return(jen.Id("bindings"), jen.Nil())
	})
	stmt.Line()
}

func sdkIconSliceValue(icons []*IconData) jen.Code {
	if len(icons) == 0 {
		return nil
	}
	values := make([]jen.Code, 0, len(icons))
	for _, icon := range icons {
		if icon == nil {
			continue
		}
		dict := jen.Dict{
			jen.Id("Source"): jen.Lit(icon.Source),
		}
		if icon.MIMEType != "" {
			dict[jen.Id("MIMEType")] = jen.Lit(icon.MIMEType)
		}
		if len(icon.Sizes) > 0 {
			sizes := make([]jen.Code, 0, len(icon.Sizes))
			for _, size := range icon.Sizes {
				sizes = append(sizes, jen.Lit(size))
			}
			dict[jen.Id("Sizes")] = jen.Index().String().Values(sizes...)
		}
		if icon.Theme != "" {
			dict[jen.Id("Theme")] = jen.Id("mcpsdk").Dot("IconTheme").Call(jen.Lit(icon.Theme))
		}
		values = append(values, jen.Id("mcpsdk").Dot("Icon").Values(dict))
	}
	if len(values) == 0 {
		return nil
	}
	return jen.Index().Id("mcpsdk").Dot("Icon").Values(values...)
}

func sdkPromptArgumentsValue(args []PromptArg) jen.Code {
	values := make([]jen.Code, 0, len(args))
	for _, arg := range args {
		values = append(values, jen.Op("&").Id("mcpsdk").Dot("PromptArgument").Values(jen.Dict{
			jen.Id("Name"):        jen.Lit(arg.Name),
			jen.Id("Description"): jen.Lit(arg.Description),
			jen.Id("Required"):    jen.Lit(arg.Required),
		}))
	}
	return jen.Index().Op("*").Id("mcpsdk").Dot("PromptArgument").Values(values...)
}
func sdkAdapterHandler(methodName, sdkHandlerType, bridgeHandler, requestType, resultType string, body ...jen.Code) *jen.Statement {
	return jen.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).Id(methodName).Params().Id("mcpsdk").Dot(sdkHandlerType).Block(
		jen.Return(jen.Id("sdkbridge").Dot(bridgeHandler).Call(
			jen.Id("a").Dot("sdkHandlerContext").Call(),
			jen.Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("request").Id("sdkbridge").Dot(requestType),
			).Params(jen.Op("*").Id("mcpsdk").Dot(resultType), jen.Error()).Block(body...),
		)),
	)
}
func sdkNamedDispatchBody(payloadType, adapterMethod, resultConverter string) []jen.Code {
	return []jen.Code{
		jen.Id("payload").Op(":=").Op("&").Id(payloadType).Values(jen.Dict{jen.Id("Name"): jen.Id("request").Dot("Name"), jen.Id("Arguments"): jen.Id("mcpJSONFromRaw").Call(jen.Id("request").Dot("Arguments"))}),
		jen.Id("ctx").Op("=").Id("request").Dot("Bind").Call(jen.Id("payload")),
		jen.List(jen.Id("result"), jen.Id("err")).Op(":=").Id("a").Dot(adapterMethod).Call(jen.Id("ctx"), jen.Id("payload")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
		jen.Return(jen.Id(resultConverter).Call(jen.Id("result"))),
	}
}

func sdkServerHandlerSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-sdk-server-handlers", func(stmt *jen.Statement) {
		stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).Id("sdkHandlerContext").Params().Id("sdkbridge").Dot("HandlerContext").Block(
			jen.Return(jen.Id("sdkbridge").Dot("HandlerContext").Values(jen.Dict{
				jen.Id("RequestStateKey"): jen.Id("a").Dot("requestStateKey"),
				jen.Id("Sessions"):        jen.Id("a").Dot("sessions"),
			})),
		)
		stmt.Line()
		stmt.Add(sdkAdapterHandler(
			"sdkToolHandler", "ToolHandler", "ToolHandler", "ToolRequest", "CallToolResult",
			sdkNamedDispatchBody("ToolsCallPayload", "ToolsCall", "sdkCallToolResult")...,
		))
		stmt.Line()
		if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
			stmt.Add(sdkAdapterHandler(
				"sdkPromptHandler", "PromptHandler", "PromptHandler", "PromptRequest", "GetPromptResult",
				sdkNamedDispatchBody("PromptsGetPayload", "PromptsGet", "sdkGetPromptResult")...,
			))
			stmt.Line()
			stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).Id("sdkCompletionHandler").Params().Func().Params(jen.Qual("context", "Context"), jen.Op("*").Id("mcpsdk").Dot("CompleteRequest")).Params(jen.Op("*").Id("mcpsdk").Dot("CompleteResult"), jen.Error()).Block(
				jen.Return(jen.Func().Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("req").Op("*").Id("mcpsdk").Dot("CompleteRequest")).Params(jen.Op("*").Id("mcpsdk").Dot("CompleteResult"), jen.Error()).BlockFunc(func(g *jen.Group) {
					g.Id("ctx").Op("=").Id("sdkbridge").Dot("BindCompletionContext").Call(jen.Id("ctx"), jen.Id("req"), jen.Id("a").Dot("sdkHandlerContext").Call())
					g.If(jen.Id("req").Op("==").Nil().Op("||").Id("req").Dot("Params").Op("==").Nil().Op("||").Id("req").Dot("Params").Dot("Ref").Op("==").Nil()).Block(jen.Return(jen.Id("sdkCompleteValues").Call(jen.Nil(), jen.Lit(0), jen.False()), jen.Nil()))
					g.If(jen.Id("req").Dot("Params").Dot("Ref").Dot("Type").Op("!=").Lit("ref/prompt")).Block(jen.Return(jen.Id("sdkCompleteValues").Call(jen.Nil(), jen.Lit(0), jen.False()), jen.Nil()))
					g.Id("_").Op("=").Id("ctx")
					g.Switch(jen.Id("req").Dot("Params").Dot("Ref").Dot("Name")).BlockFunc(func(sg *jen.Group) {
						for _, prompt := range data.DynamicPrompts {
							sg.Case(jen.Lit(prompt.Name)).BlockFunc(func(cg *jen.Group) {
								cg.Switch(jen.Id("req").Dot("Params").Dot("Argument").Dot("Name")).BlockFunc(func(argg *jen.Group) {
									for _, arg := range prompt.Arguments {
										if len(arg.Values) == 0 {
											continue
										}
										values := make([]jen.Code, 0, len(arg.Values))
										for _, value := range arg.Values {
											values = append(values, jen.Lit(value))
										}
										argg.Case(jen.Lit(arg.Name)).Block(jen.Return(jen.Id("sdkFilteredCompletion").Call(jen.Id("req").Dot("Params").Dot("Argument").Dot("Value"), jen.Index().String().Values(values...)), jen.Nil()))
									}
									argg.Default().Block(jen.Return(jen.Id("sdkCompleteValues").Call(jen.Nil(), jen.Lit(0), jen.False()), jen.Nil()))
								})
							})
						}
						sg.Default().Block(jen.Return(jen.Id("sdkCompleteValues").Call(jen.Nil(), jen.Lit(0), jen.False()), jen.Nil()))
					})
				})),
			)
			stmt.Line()
		}
		if len(data.Resources) > 0 || len(data.SkillDirectories) > 0 {
			stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).Id("sdkResourceHandler").Params().Id("mcpsdk").Dot("ResourceHandler").Block(
				jen.Return(jen.Id("sdkbridge").Dot("ResourceHandler").Call(jen.Id("a").Dot("sdkHandlerContext").Call(), jen.Func().Params(
					jen.Id("ctx").Qual("context", "Context"), jen.Id("request").Id("sdkbridge").Dot("ResourceRequest"),
				).Params(jen.Op("*").Id("mcpsdk").Dot("ReadResourceResult"), jen.Error()).Block(
					jen.Id("payload").Op(":=").Op("&").Id("ResourcesReadPayload").Values(jen.Dict{jen.Id("URI"): jen.Id("request").Dot("URI")}),
					jen.Id("ctx").Op("=").Id("request").Dot("Bind").Call(jen.Id("payload")),
					jen.List(jen.Id("result"), jen.Id("err")).Op(":=").Id("a").Dot("ResourcesRead").Call(jen.Id("ctx"), jen.Id("payload")),
					jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
					jen.Return(jen.Id("sdkReadResourceResult").Call(jen.Id("result"), jen.Id("request").Dot("URI"))),
				))),
			)
			stmt.Line()
		}
	})
}

func sdkServerConversionSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-sdk-server-conversions", func(stmt *jen.Statement) {
		emitSDKCallToolResult(stmt)
		if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
			emitSDKPromptConversion(stmt)
		}
		if adapterDataHasResources(data) {
			emitSDKReadResourceConversion(stmt)
		}
		if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
			emitSDKCompletionConversion(stmt)
		}
		emitSDKContentConversions(stmt)
		emitSDKHelpers(stmt)
	})
}

func emitSDKCallToolResult(stmt *jen.Statement) {
	stmt.Func().Id("sdkCallToolResult").
		Params(jen.Id("result").Op("*").Id("ToolsCallResult")).
		Params(jen.Op("*").Id("mcpsdk").Dot("CallToolResult"), jen.Error()).
		Block(
			jen.If(jen.Id("result").Op("==").Nil()).Block(
				jen.Return(jen.Op("&").Id("mcpsdk").Dot("CallToolResult").Values(jen.Dict{
					jen.Id("Content"): jen.Index().Id("mcpsdk").Dot("Content").Values(),
				}), jen.Nil()),
			),
			jen.Id("content").Op(":=").Make(jen.Index().Id("mcpsdk").Dot("Content"), jen.Lit(0), jen.Len(jen.Id("result").Dot("Content"))),
			jen.For(jen.List(jen.Id("_"), jen.Id("item")).Op(":=").Range().Id("result").Dot("Content")).Block(
				jen.List(jen.Id("converted"), jen.Id("err")).Op(":=").Id("sdkContentFromItem").Call(jen.Id("item")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
				jen.Id("content").Op("=").Append(jen.Id("content"), jen.Id("converted")),
			),
			jen.Id("callResult").Op(":=").Op("&").Id("mcpsdk").Dot("CallToolResult").Values(jen.Dict{
				jen.Id("Content"): jen.Id("content"),
			}),
			jen.List(jen.Id("structuredContent"), jen.Id("err")).Op(":=").Id("mcpJSONRaw").Call(jen.Id("result").Dot("StructuredContent")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
			jen.If(jen.Len(jen.Id("structuredContent")).Op(">").Lit(0)).Block(
				jen.Id("callResult").Dot("StructuredContent").Op("=").Id("structuredContent"),
			),
			jen.If(jen.Id("result").Dot("IsError").Op("!=").Nil()).Block(
				jen.Id("callResult").Dot("IsError").Op("=").Op("*").Id("result").Dot("IsError"),
			),
			jen.Return(jen.Id("callResult"), jen.Nil()),
		)
	stmt.Line()
}

func emitSDKPromptConversion(stmt *jen.Statement) {
	stmt.Func().Id("sdkGetPromptResult").
		Params(jen.Id("result").Op("*").Id("PromptsGetResult")).
		Params(jen.Op("*").Id("mcpsdk").Dot("GetPromptResult"), jen.Error()).
		Block(
			jen.If(jen.Id("result").Op("==").Nil()).Block(
				jen.Return(jen.Op("&").Id("mcpsdk").Dot("GetPromptResult").Values(jen.Dict{
					jen.Id("Messages"): jen.Index().Op("*").Id("mcpsdk").Dot("PromptMessage").Values(),
				}), jen.Nil()),
			),
			jen.Id("messages").Op(":=").Make(jen.Index().Op("*").Id("mcpsdk").Dot("PromptMessage"), jen.Lit(0), jen.Len(jen.Id("result").Dot("Messages"))),
			jen.For(jen.List(jen.Id("_"), jen.Id("message")).Op(":=").Range().Id("result").Dot("Messages")).Block(
				jen.List(jen.Id("converted"), jen.Id("err")).Op(":=").Id("sdkPromptMessage").Call(jen.Id("message")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
				jen.Id("messages").Op("=").Append(jen.Id("messages"), jen.Id("converted")),
			),
			jen.Return(jen.Op("&").Id("mcpsdk").Dot("GetPromptResult").Values(jen.Dict{
				jen.Id("Description"): jen.Id("derefString").Call(jen.Id("result").Dot("Description")),
				jen.Id("Messages"):    jen.Id("messages"),
			}), jen.Nil()),
		)
	stmt.Line()
	stmt.Func().Id("sdkPromptMessage").
		Params(jen.Id("message").Op("*").Id("PromptMessage")).
		Params(jen.Op("*").Id("mcpsdk").Dot("PromptMessage"), jen.Error()).
		Block(
			jen.If(jen.Id("message").Op("==").Nil()).Block(
				jen.Return(jen.Op("&").Id("mcpsdk").Dot("PromptMessage").Values(jen.Dict{
					jen.Id("Content"): jen.Op("&").Id("mcpsdk").Dot("TextContent").Values(),
				}), jen.Nil()),
			),
			jen.List(jen.Id("content"), jen.Id("err")).Op(":=").Id("sdkContentFromMessageContent").Call(jen.Id("message").Dot("Content")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
			jen.Return(jen.Op("&").Id("mcpsdk").Dot("PromptMessage").Values(jen.Dict{
				jen.Id("Role"):    jen.Id("mcpsdk").Dot("Role").Call(jen.Id("message").Dot("Role")),
				jen.Id("Content"): jen.Id("content"),
			}), jen.Nil()),
		)
	stmt.Line()
	stmt.Func().Id("sdkContentFromMessageContent").
		Params(jen.Id("item").Op("*").Id("MessageContent")).
		Params(jen.Id("mcpsdk").Dot("Content"), jen.Error()).
		Block(
			jen.If(jen.Id("item").Op("==").Nil()).Block(
				jen.Return(jen.Op("&").Id("mcpsdk").Dot("TextContent").Values(), jen.Nil()),
			),
			jen.Id("contentItem").Op(":=").Op("&").Id("ContentItem").Values(jen.Dict{
				jen.Id("Type"):     jen.Id("item").Dot("Type"),
				jen.Id("Text"):     jen.Id("item").Dot("Text"),
				jen.Id("Data"):     jen.Id("item").Dot("Data"),
				jen.Id("MimeType"): jen.Id("item").Dot("MimeType"),
				jen.Id("URI"):      jen.Id("item").Dot("URI"),
				jen.Id("Meta"):     jen.Id("item").Dot("Meta"),
			}),
			jen.Return(jen.Id("sdkContentFromItem").Call(jen.Id("contentItem"))),
		)
	stmt.Line()
}

func emitSDKCompletionConversion(stmt *jen.Statement) {
	stmt.Func().Id("sdkFilteredCompletion").
		Params(jen.Id("prefix").String(), jen.Id("values").Index().String()).
		Op("*").Id("mcpsdk").Dot("CompleteResult").
		Block(
			jen.Id("matches").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Len(jen.Id("values"))),
			jen.For(jen.List(jen.Id("_"), jen.Id("value")).Op(":=").Range().Id("values")).Block(
				jen.If(jen.Id("prefix").Op("==").Lit("").Op("||").Qual("strings", "HasPrefix").Call(jen.Id("value"), jen.Id("prefix"))).Block(
					jen.Id("matches").Op("=").Append(jen.Id("matches"), jen.Id("value")),
				),
			),
			jen.Id("total").Op(":=").Len(jen.Id("matches")),
			jen.Id("hasMore").Op(":=").False(),
			jen.If(jen.Len(jen.Id("matches")).Op(">").Lit(100)).Block(
				jen.Id("matches").Op("=").Id("matches").Index(jen.Empty(), jen.Lit(100)),
				jen.Id("hasMore").Op("=").True(),
			),
			jen.Return(jen.Id("sdkCompleteValues").Call(jen.Id("matches"), jen.Id("total"), jen.Id("hasMore"))),
		)
	stmt.Line()
	stmt.Func().Id("sdkCompleteValues").
		Params(jen.Id("values").Index().String(), jen.Id("total").Int(), jen.Id("hasMore").Bool()).
		Op("*").Id("mcpsdk").Dot("CompleteResult").
		Block(
			jen.If(jen.Id("values").Op("==").Nil()).Block(
				jen.Id("values").Op("=").Index().String().Values(),
			),
			jen.Return(jen.Op("&").Id("mcpsdk").Dot("CompleteResult").Values(jen.Dict{
				jen.Id("Completion"): jen.Id("mcpsdk").Dot("CompletionResultDetails").Values(jen.Dict{
					jen.Id("Values"):  jen.Id("values"),
					jen.Id("Total"):   jen.Id("total"),
					jen.Id("HasMore"): jen.Id("hasMore"),
				}),
			})),
		)
	stmt.Line()
}

func emitSDKReadResourceConversion(stmt *jen.Statement) {
	stmt.Func().Id("sdkReadResourceResult").
		Params(jen.Id("result").Op("*").Id("ResourcesReadResult"), jen.Id("uri").String()).
		Params(jen.Op("*").Id("mcpsdk").Dot("ReadResourceResult"), jen.Error()).
		Block(
			jen.If(jen.Id("result").Op("==").Nil()).Block(
				jen.Return(jen.Op("&").Id("mcpsdk").Dot("ReadResourceResult").Values(jen.Dict{
					jen.Id("Contents"): jen.Index().Op("*").Id("mcpsdk").Dot("ResourceContents").Values(),
				}), jen.Nil()),
			),
			jen.Id("contents").Op(":=").Make(jen.Index().Op("*").Id("mcpsdk").Dot("ResourceContents"), jen.Lit(0), jen.Len(jen.Id("result").Dot("Contents"))),
			jen.For(jen.List(jen.Id("_"), jen.Id("content")).Op(":=").Range().Id("result").Dot("Contents")).Block(
				jen.List(jen.Id("converted"), jen.Id("err")).Op(":=").Id("sdkReadResourceContent").Call(jen.Id("content"), jen.Id("uri")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
				jen.Id("contents").Op("=").Append(jen.Id("contents"), jen.Id("converted")),
			),
			jen.Return(jen.Op("&").Id("mcpsdk").Dot("ReadResourceResult").Values(jen.Dict{
				jen.Id("Contents"): jen.Id("contents"),
			}), jen.Nil()),
		)
	stmt.Line()
	stmt.Func().Id("sdkReadResourceContent").
		Params(jen.Id("item").Op("*").Id("ResourceContent"), jen.Id("uri").String()).
		Params(jen.Op("*").Id("mcpsdk").Dot("ResourceContents"), jen.Error()).
		Block(
			jen.If(jen.Id("item").Op("==").Nil()).Block(
				jen.Return(jen.Op("&").Id("mcpsdk").Dot("ResourceContents").Values(jen.Dict{jen.Id("URI"): jen.Id("uri")}), jen.Nil()),
			),
			jen.List(jen.Id("meta"), jen.Id("err")).Op(":=").Id("sdkMeta").Call(jen.Id("item").Dot("Meta")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
			jen.Id("resource").Op(":=").Op("&").Id("mcpsdk").Dot("ResourceContents").Values(jen.Dict{
				jen.Id("URI"):      jen.Id("uri"),
				jen.Id("MIMEType"): jen.Id("derefString").Call(jen.Id("item").Dot("MimeType")),
				jen.Id("Text"):     jen.Id("derefString").Call(jen.Id("item").Dot("Text")),
				jen.Id("Meta"):     jen.Id("meta"),
			}),
			jen.If(jen.Id("item").Dot("Blob").Op("!=").Nil()).Block(
				jen.List(jen.Id("data"), jen.Id("err")).Op(":=").Id("sdkDecodeBase64").Call(jen.Id("item").Dot("Blob")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
				jen.Id("resource").Dot("Blob").Op("=").Id("data"),
			),
			jen.Return(jen.Id("resource"), jen.Nil()),
		)
	stmt.Line()
}

func sdkBinaryContentCase(contentType, sdkType string) *jen.Statement {
	return jen.Case(jen.Lit(contentType)).Block(
		jen.List(jen.Id("data"), jen.Id("err")).Op(":=").Id("sdkDecodeBase64").Call(jen.Id("item").Dot("Data")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
		jen.Return(jen.Op("&").Id("mcpsdk").Dot(sdkType).Values(jen.Dict{
			jen.Id("Data"):     jen.Id("data"),
			jen.Id("MIMEType"): jen.Id("derefString").Call(jen.Id("item").Dot("MimeType")),
			jen.Id("Meta"):     jen.Id("meta"),
		}), jen.Nil()),
	)
}
func emitSDKContentConversions(stmt *jen.Statement) {
	stmt.Func().Id("sdkContentFromItem").
		Params(jen.Id("item").Op("*").Id("ContentItem")).
		Params(jen.Id("mcpsdk").Dot("Content"), jen.Error()).
		Block(
			jen.If(jen.Id("item").Op("==").Nil()).Block(
				jen.Return(jen.Op("&").Id("mcpsdk").Dot("TextContent").Values(), jen.Nil()),
			),
			jen.List(jen.Id("meta"), jen.Id("err")).Op(":=").Id("sdkMeta").Call(jen.Id("item").Dot("Meta")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
			jen.Switch(jen.Id("item").Dot("Type")).Block(
				jen.Case(jen.Lit("text")).Block(
					jen.Return(jen.Op("&").Id("mcpsdk").Dot("TextContent").Values(jen.Dict{
						jen.Id("Text"): jen.Id("derefString").Call(jen.Id("item").Dot("Text")),
						jen.Id("Meta"): jen.Id("meta"),
					}), jen.Nil()),
				),
				sdkBinaryContentCase("image", "ImageContent"),
				sdkBinaryContentCase("audio", "AudioContent"),
				jen.Case(jen.Lit("resource")).Block(
					jen.List(jen.Id("resource"), jen.Id("err")).Op(":=").Id("sdkResourceContents").Call(jen.Id("item"), jen.Id("meta")),
					jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
					jen.Return(jen.Op("&").Id("mcpsdk").Dot("EmbeddedResource").Values(jen.Dict{jen.Id("Resource"): jen.Id("resource"), jen.Id("Meta"): jen.Id("meta")}), jen.Nil()),
				),
				jen.Default().Block(
					jen.If(jen.Id("item").Dot("URI").Op("!=").Nil()).Block(
						jen.List(jen.Id("resource"), jen.Id("err")).Op(":=").Id("sdkResourceContents").Call(jen.Id("item"), jen.Id("meta")),
						jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
						jen.Return(jen.Op("&").Id("mcpsdk").Dot("EmbeddedResource").Values(jen.Dict{jen.Id("Resource"): jen.Id("resource"), jen.Id("Meta"): jen.Id("meta")}), jen.Nil()),
					),
					jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("unsupported MCP content type %q"), jen.Id("item").Dot("Type"))),
				),
			),
		)
	stmt.Line()

	stmt.Func().Id("sdkResourceContents").
		Params(jen.Id("item").Op("*").Id("ContentItem"), jen.Id("meta").Id("mcpsdk").Dot("Meta")).
		Params(jen.Op("*").Id("mcpsdk").Dot("ResourceContents"), jen.Error()).
		Block(
			jen.Id("resource").Op(":=").Op("&").Id("mcpsdk").Dot("ResourceContents").Values(jen.Dict{
				jen.Id("URI"):      jen.Id("derefString").Call(jen.Id("item").Dot("URI")),
				jen.Id("MIMEType"): jen.Id("derefString").Call(jen.Id("item").Dot("MimeType")),
				jen.Id("Meta"):     jen.Id("meta"),
				jen.Id("Text"):     jen.Id("derefString").Call(jen.Id("item").Dot("Text")),
			}),
			jen.If(jen.Id("item").Dot("Data").Op("!=").Nil()).Block(
				jen.List(jen.Id("data"), jen.Id("err")).Op(":=").Id("sdkDecodeBase64").Call(jen.Id("item").Dot("Data")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
				jen.Id("resource").Dot("Blob").Op("=").Id("data"),
			),
			jen.Return(jen.Id("resource"), jen.Nil()),
		)
	stmt.Line()
}

func emitSDKHelpers(stmt *jen.Statement) {
	stmt.Func().Id("sdkMeta").
		Params(jen.Id("value").Any()).
		Params(jen.Id("mcpsdk").Dot("Meta"), jen.Error()).
		Block(
			jen.Return(jen.Id("sdkbridge").Dot("DecodeMeta").Call(jen.Id("value"))),
		)
	stmt.Line()

	stmt.Func().Id("sdkDecodeBase64").
		Params(jen.Id("raw").Op("*").String()).
		Params(jen.Index().Byte(), jen.Error()).
		Block(
			jen.If(jen.Id("raw").Op("==").Nil().Op("||").Op("*").Id("raw").Op("==").Lit("")).Block(
				jen.Return(jen.Nil(), jen.Nil()),
			),
			jen.List(jen.Id("data"), jen.Id("err")).Op(":=").Qual("encoding/base64", "StdEncoding").Dot("DecodeString").Call(jen.Op("*").Id("raw")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
			jen.Return(jen.Id("data"), jen.Nil()),
		)
	stmt.Line()
	stmt.Func().Id("derefString").Params(jen.Id("s").Op("*").String()).String().Block(
		jen.If(jen.Id("s").Op("==").Nil()).Block(jen.Return(jen.Lit(""))),
		jen.Return(jen.Op("*").Id("s")),
	)
	stmt.Line()
	stmt.Func().Id("boolPtr").Params(jen.Id("v").Bool()).Op("*").Bool().Block(
		jen.Return(jen.Op("&").Id("v")),
	)
	stmt.Line()
}
