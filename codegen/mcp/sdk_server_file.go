package codegen

import (
	"path/filepath"
	"sort"

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
		{Path: "net/url"},
		{Path: "slices"},
		{Path: "sync"},
		{Path: genpkg + "/" + svcName, Name: svcName},
		{Path: "github.com/modelcontextprotocol/go-sdk/auth", Name: "mcpauth"},
		{Path: "github.com/modelcontextprotocol/go-sdk/mcp", Name: "mcpsdk"},
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/mcp", Name: "mcpruntime"},
		{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/mcp/sdkclient", Name: "sdkclient"},
		{Path: "github.com/CaliLuke/loom/http", Name: "loomhttp"},
		{Path: "github.com/CaliLuke/loom/observability/transport"},
	}
	if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
		// strings is only referenced by the prompt completion helpers.
		sdkServerImports = append(sdkServerImports, &codegen.ImportSpec{Path: "strings"})
	}
	if len(data.SkillDirectories) > 0 {
		sdkServerImports = append(sdkServerImports, &codegen.ImportSpec{
			Path: "github.com/CaliLuke/loom-mcp/v2/runtime/mcp/skills",
			Name: "mcpskills",
		})
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
		sdkServerImports = append(sdkServerImports, &codegen.ImportSpec{
			Path: projectedImports[name],
			Name: name,
		})
	}
	sections := []codegen.Section{
		codegen.Header("SDK-backed MCP server for "+svc.Name+" service", pkgName, sdkServerImports),
		sdkServerTypesSection(data),
		sdkServerConstructorSection(data),
		sdkServerHTTPSection(),
		sdkServerSessionErrorSection(),
		sdkServerRegistrationSection(data),
		sdkServerHandlerSection(data),
		sdkServerConversionSection(data),
	}
	return &codegen.File{
		Path:     filepath.Join(codegen.Gendir, "mcp_"+svcName, "sdk_server.go"),
		Sections: sections,
	}
}

func sdkServerTypesSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-sdk-server-types", func(stmt *jen.Statement) {
		stmt.Comment("SDK-backed MCP streamable HTTP server.").Line()
		stmt.Type().Id("SDKServer").Struct(
			jen.Id("Handler").Qual("net/http", "Handler"),
			jen.Id("Adapter").Op("*").Id("MCPAdapter"),
			jen.Id("Server").Op("*").Id("mcpsdk").Dot("Server"),
		)
		stmt.Line()
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
			g.Id("StreamableHTTP").Op("*").Id("mcpsdk").Dot("StreamableHTTPOptions")
		})
		stmt.Line()
		stmt.Type().Id("sdkResponseObserver").Struct(
			jen.Qual("net/http", "ResponseWriter"),
			jen.Id("statusCode").Int(),
			jen.Id("onSessionIssued").Func().Params(jen.String()),
			jen.Id("sessionOnce").Qual("sync", "Once"),
		)
		stmt.Line()
	})
}

func sdkServerConstructorSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-sdk-server-constructor", func(stmt *jen.Statement) {
		stmt.Func().Id("NewSDKServer").
			Params(
				jen.Id("service").Id(data.Package).Dot("Service"),
				jen.Id("opts").Op("*").Id("SDKServerOptions"),
			).
			Params(jen.Op("*").Id("SDKServer"), jen.Error()).
			BlockFunc(func(g *jen.Group) {
				g.Var().Id("adapterOpts").Op("*").Id("MCPAdapterOptions")
				g.Var().Id("requestContext").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Qual("net/http", "Request")).Qual("context", "Context")
				g.Var().Id("requestStateKey").Index().Byte()
				g.Var().Id("transportObserver").Qual("github.com/CaliLuke/loom/observability/transport", "Observer")
				g.Var().Id("runtimeCORS").Op("*").Id("loomhttp").Dot("RuntimeCORSPolicy")
				if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
					g.Var().Id("promptProvider").Id("PromptProvider")
				}
				g.Var().Id("serverOpts").Op("*").Id("mcpsdk").Dot("ServerOptions")
				g.Var().Id("streamableOpts").Op("*").Id("mcpsdk").Dot("StreamableHTTPOptions")
				g.If(jen.Id("opts").Op("!=").Nil()).BlockFunc(func(ifg *jen.Group) {
					ifg.Id("adapterOpts").Op("=").Id("opts").Dot("Adapter")
					ifg.Id("requestContext").Op("=").Id("opts").Dot("RequestContext")
					ifg.Id("requestStateKey").Op("=").Id("opts").Dot("RequestStateKey")
					ifg.Id("transportObserver").Op("=").Id("opts").Dot("TransportObserver")
					ifg.Id("runtimeCORS").Op("=").Id("opts").Dot("RuntimeCORS")
					if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
						ifg.Id("promptProvider").Op("=").Id("opts").Dot("PromptProvider")
					}
					ifg.Id("serverOpts").Op("=").Id("opts").Dot("Server")
					ifg.Id("streamableOpts").Op("=").Id("opts").Dot("StreamableHTTP")
				})
				g.If(jen.Id("adapterOpts").Op("!=").Nil().Op("&&").Id("adapterOpts").Dot("ToolSearch").Op("!=").Nil().Op("&&").Id("adapterOpts").Dot("ToolSearch").Dot("AllowDirectHiddenCalls")).Block(
					jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("SDK ToolSearch compact mode does not support AllowDirectHiddenCalls"))),
				)
				if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
					g.Id("adapter").Op(":=").Id("NewMCPAdapter").Call(jen.Id("service"), jen.Id("promptProvider"), jen.Id("adapterOpts"))
					g.Id("serverOpts").Op("=").Id("sdkServerOptionsWithCompletion").Call(
						jen.Id("serverOpts"),
						jen.Id("adapter").Dot("sdkCompletionHandler").Call(jen.Id("requestContext")),
					)
				} else {
					g.Id("adapter").Op(":=").Id("NewMCPAdapter").Call(jen.Id("service"), jen.Id("adapterOpts"))
				}
				g.Id("adapter").Dot("requestStateKey").Op("=").Qual("slices", "Clone").Call(jen.Id("requestStateKey"))
				g.Id("serverOpts").Op("=").Id("sdkServerOptionsWithDefaults").Call(jen.Id("serverOpts"))
				if data.HasWatchableResources {
					g.If(jen.Id("streamableOpts").Op("!=").Nil().Op("&&").Id("streamableOpts").Dot("Stateless")).Block(
						jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("watchable MCP resources require stateful Streamable HTTP sessions"))),
					)
					g.Id("serverOpts").Op("=").Id("sdkServerOptionsWithSubscriptions").Call(jen.Id("serverOpts"))
				}
				g.Id("server").Op(":=").Id("mcpsdk").Dot("NewServer").Call(
					jen.Op("&").Id("mcpsdk").Dot("Implementation").Values(sdkImplementationDict(data)),
					jen.Id("serverOpts"),
				)
				g.If(jen.Id("err").Op(":=").Id("registerSDKTools").Call(jen.Id("server"), jen.Id("adapter"), jen.Id("requestContext")), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				)
				g.If(jen.Id("err").Op(":=").Id("registerSDKResources").Call(jen.Id("server"), jen.Id("adapter"), jen.Id("requestContext")), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				)
				g.If(jen.Id("err").Op(":=").Id("registerSDKPrompts").Call(jen.Id("server"), jen.Id("adapter"), jen.Id("requestContext")), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				)
				g.Id("handler").Op(":=").Id("newSDKHandler").Call(jen.Id("server"), jen.Id("adapter"), jen.Id("requestContext"), jen.Id("streamableOpts"))
				g.If(jen.Id("transportObserver").Op("!=").Nil()).Block(
					jen.Id("handler").Op("=").Id("transport").Dot("HTTPMiddleware").Call(jen.Id("transportObserver")).Call(jen.Id("handler")),
				)
				g.If(jen.Id("runtimeCORS").Op("!=").Nil()).Block(
					jen.Id("handler").Op("=").Id("sdkRuntimeCORSHandler").Call(jen.Id("handler"), jen.Op("*").Id("runtimeCORS")),
				)
				g.Return(
					jen.Op("&").Id("SDKServer").Values(jen.Dict{
						jen.Id("Handler"): jen.Id("handler"),
						jen.Id("Adapter"): jen.Id("adapter"),
						jen.Id("Server"):  jen.Id("server"),
					}),
					jen.Nil(),
				)
			})
		stmt.Line()
		if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
			stmt.Func().Id("sdkServerOptionsWithCompletion").
				Params(
					jen.Id("opts").Op("*").Id("mcpsdk").Dot("ServerOptions"),
					jen.Id("handler").Func().
						Params(jen.Qual("context", "Context"), jen.Op("*").Id("mcpsdk").Dot("CompleteRequest")).
						Params(jen.Op("*").Id("mcpsdk").Dot("CompleteResult"), jen.Error()),
				).
				Op("*").Id("mcpsdk").Dot("ServerOptions").
				Block(
					jen.If(jen.Id("opts").Op("==").Nil()).Block(
						jen.Id("opts").Op("=").Op("&").Id("mcpsdk").Dot("ServerOptions").Values(),
					).Else().Block(
						jen.Id("copied").Op(":=").Op("*").Id("opts"),
						jen.Id("opts").Op("=").Op("&").Id("copied"),
					),
					jen.If(jen.Id("opts").Dot("CompletionHandler").Op("==").Nil()).Block(
						jen.Id("opts").Dot("CompletionHandler").Op("=").Id("handler"),
					),
					jen.Return(jen.Id("opts")),
				)
			stmt.Line()
		}

		emitSDKServerOptionsWithDefaults(stmt)
		if data.HasWatchableResources {
			emitSDKServerOptionsWithSubscriptions(stmt, data)
		}
		emitSDKResourceUpdated(stmt, data)

		stmt.Func().Params(jen.Id("w").Op("*").Id("sdkResponseObserver")).
			Id("captureSession").
			Params().
			Block(
				jen.If(jen.Id("w").Op("==").Nil().Op("||").Id("w").Dot("onSessionIssued").Op("==").Nil()).Block(
					jen.Return(),
				),
				jen.Id("sessionID").Op(":=").Id("w").Dot("Header").Call().Dot("Get").Call(jen.Id("mcpruntime").Dot("HeaderKeySessionID")),
				jen.If(jen.Id("sessionID").Op("==").Lit("")).Block(
					jen.Return(),
				),
				jen.Id("w").Dot("sessionOnce").Dot("Do").Call(jen.Func().Params().Block(
					jen.Id("w").Dot("onSessionIssued").Call(jen.Id("sessionID")),
				)),
			)
		stmt.Line()
		stmt.Func().Params(jen.Id("w").Op("*").Id("sdkResponseObserver")).
			Id("Unwrap").
			Params().
			Qual("net/http", "ResponseWriter").
			Block(
				jen.Return(jen.Id("w").Dot("ResponseWriter")),
			)
		stmt.Line()
		stmt.Func().Params(jen.Id("w").Op("*").Id("sdkResponseObserver")).
			Id("WriteHeader").
			Params(jen.Id("statusCode").Int()).
			Block(
				jen.Id("w").Dot("statusCode").Op("=").Id("statusCode"),
				jen.If(jen.Id("statusCode").Op("<").Qual("net/http", "StatusBadRequest")).Block(
					jen.Id("w").Dot("captureSession").Call(),
				),
				jen.Id("w").Dot("ResponseWriter").Dot("WriteHeader").Call(jen.Id("statusCode")),
			)
		stmt.Line()
		stmt.Func().Params(jen.Id("w").Op("*").Id("sdkResponseObserver")).
			Id("Write").
			Params(jen.Id("data").Index().Byte()).
			Params(jen.Int(), jen.Error()).
			Block(
				jen.If(jen.Id("w").Dot("statusCode").Op("==").Lit(0)).Block(
					jen.Id("w").Dot("statusCode").Op("=").Qual("net/http", "StatusOK"),
				),
				jen.If(jen.Id("w").Dot("statusCode").Op("<").Qual("net/http", "StatusBadRequest")).Block(
					jen.Id("w").Dot("captureSession").Call(),
				),
				jen.Return(jen.Id("w").Dot("ResponseWriter").Dot("Write").Call(jen.Id("data"))),
			)
		stmt.Line()
	})
}

func emitSDKServerOptionsWithSubscriptions(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Id("sdkServerOptionsWithSubscriptions").
		Params(jen.Id("opts").Op("*").Id("mcpsdk").Dot("ServerOptions")).
		Op("*").Id("mcpsdk").Dot("ServerOptions").
		BlockFunc(func(g *jen.Group) {
			g.Id("subscribe").Op(":=").Id("opts").Dot("SubscribeHandler")
			g.Id("unsubscribe").Op(":=").Id("opts").Dot("UnsubscribeHandler")
			emitSDKSubscriptionHandler(g, "SubscribeHandler", "SubscribeRequest", "subscribe", "sdkSubscriptionURI")
			emitSDKSubscriptionHandler(g, "UnsubscribeHandler", "UnsubscribeRequest", "unsubscribe", "sdkUnsubscriptionURI")
			g.Return(jen.Id("opts"))
		})
	stmt.Line()
	stmt.Func().Id("sdkWatchableResourceURI").Params(jen.Id("uri").String()).Bool().Block(
		jen.Switch(jen.Id("uri")).BlockFunc(func(g *jen.Group) {
			for _, resource := range data.Resources {
				if resource.Watchable {
					g.Case(jen.Lit(resource.URI)).Block(jen.Return(jen.True()))
				}
			}
			g.Default().Block(jen.Return(jen.False()))
		}),
	)
	stmt.Line()
	stmt.Func().Id("sdkSubscriptionURI").Params(jen.Id("req").Op("*").Id("mcpsdk").Dot("SubscribeRequest")).String().Block(
		jen.If(jen.Id("req").Op("==").Nil().Op("||").Id("req").Dot("Params").Op("==").Nil()).Block(jen.Return(jen.Lit(""))),
		jen.Return(jen.Id("req").Dot("Params").Dot("URI")),
	)
	stmt.Line()
	stmt.Func().Id("sdkUnsubscriptionURI").Params(jen.Id("req").Op("*").Id("mcpsdk").Dot("UnsubscribeRequest")).String().Block(
		jen.If(jen.Id("req").Op("==").Nil().Op("||").Id("req").Dot("Params").Op("==").Nil()).Block(jen.Return(jen.Lit(""))),
		jen.Return(jen.Id("req").Dot("Params").Dot("URI")),
	)
	stmt.Line()
}

func emitSDKSubscriptionHandler(g *jen.Group, field string, requestType string, next string, uriHelper string) {
	g.Id("opts").Dot(field).Op("=").Func().
		Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("req").Op("*").Id("mcpsdk").Dot(requestType)).
		Error().
		Block(
			jen.If(jen.Id("req").Op("==").Nil().Op("||").Id("req").Dot("Params").Op("==").Nil().Op("||").Op("!").Id("sdkWatchableResourceURI").Call(jen.Id("req").Dot("Params").Dot("URI"))).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unknown watchable MCP resource %q"), jen.Id(uriHelper).Call(jen.Id("req")))),
			),
			jen.If(jen.Id(next).Op("!=").Nil()).Block(
				jen.Return(jen.Id(next).Call(jen.Id("ctx"), jen.Id("req"))),
			),
			jen.Return(jen.Nil()),
		)
}

func emitSDKResourceUpdated(stmt *jen.Statement, data *AdapterData) {
	stmt.Comment("ResourceUpdated notifies subscribed clients that a designed watchable resource changed.").Line()
	stmt.Func().Params(jen.Id("s").Op("*").Id("SDKServer")).Id("ResourceUpdated").
		Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("uri").String()).Error().
		BlockFunc(func(g *jen.Group) {
			g.If(jen.Id("s").Op("==").Nil().Op("||").Id("s").Dot("Server").Op("==").Nil()).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("MCP SDK server is not initialized"))),
			)
			if data.HasWatchableResources {
				g.If(jen.Op("!").Id("sdkWatchableResourceURI").Call(jen.Id("uri"))).Block(
					jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unknown watchable MCP resource %q"), jen.Id("uri"))),
				)
			} else {
				g.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unknown watchable MCP resource %q"), jen.Id("uri")))
				return
			}
			g.Return(jen.Id("s").Dot("Server").Dot("ResourceUpdated").Call(
				jen.Id("ctx"),
				jen.Op("&").Id("mcpsdk").Dot("ResourceUpdatedNotificationParams").Values(jen.Dict{jen.Id("URI"): jen.Id("uri")}),
			))
		})
	stmt.Line()
}

// emitSDKServerOptionsWithDefaults emits the helper that applies default SDK
// server capabilities (logging) without mutating caller-provided options.
//
// The SDK server advertises only standard capabilities inferred from the
// registered protocol features.
func emitSDKServerOptionsWithDefaults(stmt *jen.Statement) {
	stmt.Func().Id("sdkServerOptionsWithDefaults").
		Params(jen.Id("opts").Op("*").Id("mcpsdk").Dot("ServerOptions")).
		Op("*").Id("mcpsdk").Dot("ServerOptions").
		BlockFunc(func(g *jen.Group) {
			g.If(jen.Id("opts").Op("==").Nil()).Block(
				jen.Id("opts").Op("=").Op("&").Id("mcpsdk").Dot("ServerOptions").Values(),
			).Else().Block(
				jen.Id("copied").Op(":=").Op("*").Id("opts"),
				jen.Id("opts").Op("=").Op("&").Id("copied"),
			)
			g.If(jen.Id("opts").Dot("Capabilities").Op("==").Nil()).Block(
				jen.Id("opts").Dot("Capabilities").Op("=").Op("&").Id("mcpsdk").Dot("ServerCapabilities").Values(jen.Dict{
					jen.Id("Logging"): jen.Op("&").Id("mcpsdk").Dot("LoggingCapabilities").Values(),
				}),
			).Else().Block(
				jen.Id("capabilities").Op(":=").Op("*").Id("opts").Dot("Capabilities"),
				jen.Id("opts").Dot("Capabilities").Op("=").Op("&").Id("capabilities"),
			)
			g.Return(jen.Id("opts"))
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

func sdkServerHTTPSection() codegen.Section {
	return codegen.NewJenniferSection("mcp-sdk-server-http", func(stmt *jen.Statement) {
		stmt.Func().Id("newSDKHandler").
			Params(
				jen.Id("server").Op("*").Id("mcpsdk").Dot("Server"),
				jen.Id("adapter").Op("*").Id("MCPAdapter"),
				jen.Id("requestContext").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Qual("net/http", "Request")).Qual("context", "Context"),
				jen.Id("streamableOpts").Op("*").Id("mcpsdk").Dot("StreamableHTTPOptions"),
			).
			Qual("net/http", "Handler").
			Block(
				jen.Id("base").Op(":=").Id("mcpsdk").Dot("NewStreamableHTTPHandler").Call(
					jen.Func().Params(jen.Op("*").Qual("net/http", "Request")).Op("*").Id("mcpsdk").Dot("Server").Block(
						jen.Return(jen.Id("server")),
					),
					jen.Id("sdkStreamableHTTPOptions").Call(jen.Id("streamableOpts")),
				),
				jen.Return(
					jen.Qual("net/http", "HandlerFunc").Call(
						jen.Func().Params(jen.Id("w").Qual("net/http", "ResponseWriter"), jen.Id("r").Op("*").Qual("net/http", "Request")).Block(
							jen.Id("r").Op("=").Id("r").Dot("WithContext").Call(jen.Id("mcpruntime").Dot("WithRequestHeaders").Call(jen.Id("r").Dot("Context").Call(), jen.Id("r").Dot("Header"))),
							jen.If(jen.Id("requestContext").Op("!=").Nil()).Block(
								jen.Id("r").Op("=").Id("r").Dot("WithContext").Call(jen.Id("requestContext").Call(jen.Id("r").Dot("Context").Call(), jen.Id("r"))),
							),
							jen.If(jen.Id("sessionID").Op(":=").Id("r").Dot("Header").Dot("Get").Call(jen.Id("mcpruntime").Dot("HeaderKeySessionID")), jen.Id("sessionID").Op("!=").Lit("")).Block(
								jen.If(jen.Id("err").Op(":=").Id("adapter").Dot("assertSessionPrincipal").Call(jen.Id("r").Dot("Context").Call(), jen.Id("sessionID")), jen.Id("err").Op("!=").Nil()).Block(
									jen.Id("writeSDKSessionError").Call(jen.Id("w"), jen.Id("err")),
									jen.Return(),
								),
							),
							jen.List(jen.Id("transportObs"), jen.Id("transportW")).Op(":=").Qual("github.com/CaliLuke/loom/observability/transport", "BeginHTTPRequest").Call(
								jen.Id("r").Dot("Context").Call(),
								jen.Id("w"),
								jen.Lit("mcp"),
								jen.Id("r").Dot("Method"),
								jen.Id("r"),
							),
							jen.Defer().Id("transportObs").Dot("End").Call(),
							jen.Id("observer").Op(":=").Op("&").Id("sdkResponseObserver").Values(jen.Dict{
								jen.Id("ResponseWriter"): jen.Id("transportW"),
								jen.Id("onSessionIssued"): jen.Func().Params(jen.Id("sessionID").String()).Block(
									jen.Id("adapter").Dot("markInitializedSession").Call(jen.Id("sessionID")),
									jen.Id("adapter").Dot("captureSessionPrincipal").Call(jen.Id("r").Dot("Context").Call(), jen.Id("sessionID")),
								),
							}),
							jen.Id("base").Dot("ServeHTTP").Call(jen.Id("observer"), jen.Id("r")),
							jen.If(jen.Id("observer").Dot("statusCode").Op("<").Qual("net/http", "StatusBadRequest")).Block(
								jen.Id("observer").Dot("captureSession").Call(),
							),
							jen.If(jen.Id("r").Dot("Method").Op("==").Qual("net/http", "MethodDelete").Op("&&").Id("observer").Dot("statusCode").Op("<").Lit(400)).Block(
								jen.Id("adapter").Dot("clearSession").Call(jen.Id("r").Dot("Header").Dot("Get").Call(jen.Id("mcpruntime").Dot("HeaderKeySessionID"))),
							),
							jen.If(jen.Id("observer").Dot("statusCode").Op(">=").Lit(400)).Block(
								jen.Id("transportObs").Dot("Fail").Call(jen.Qual("github.com/CaliLuke/loom/observability/transport", "ReasonHandlerError")),
							),
						),
					),
				),
			)
		stmt.Line()

		stmt.Func().Id("sdkStreamableHTTPOptions").
			Params(jen.Id("opts").Op("*").Id("mcpsdk").Dot("StreamableHTTPOptions")).
			Op("*").Id("mcpsdk").Dot("StreamableHTTPOptions").
			Block(
				jen.If(jen.Id("opts").Op("==").Nil()).Block(
					jen.Return(jen.Op("&").Id("mcpsdk").Dot("StreamableHTTPOptions").Values(jen.Dict{
						jen.Id("CrossOriginProtection"): jen.Qual("net/http", "NewCrossOriginProtection").Call(),
					})),
				),
				jen.Id("configured").Op(":=").Op("*").Id("opts"),
				jen.If(jen.Id("configured").Dot("CrossOriginProtection").Op("==").Nil()).Block(
					jen.Id("configured").Dot("CrossOriginProtection").Op("=").Qual("net/http", "NewCrossOriginProtection").Call(),
				),
				jen.Return(jen.Op("&").Id("configured")),
			)
		stmt.Line()
		stmt.Func().Id("sdkRuntimeCORSHandler").
			Params(
				jen.Id("next").Qual("net/http", "Handler"),
				jen.Id("policy").Id("loomhttp").Dot("RuntimeCORSPolicy"),
			).
			Qual("net/http", "Handler").
			Block(
				jen.Id("actual").Op(":=").Id("policy").Dot("Handler").Call(jen.Id("next").Dot("ServeHTTP")),
				jen.Return(jen.Qual("net/http", "HandlerFunc").Call(
					jen.Func().Params(
						jen.Id("w").Qual("net/http", "ResponseWriter"),
						jen.Id("r").Op("*").Qual("net/http", "Request"),
					).Block(
						jen.If(jen.Id("r").Dot("Method").Op("==").Qual("net/http", "MethodOptions")).Block(
							jen.Id("policy").Dot("HandlePreflight").Call(
								jen.Id("w"),
								jen.Id("r"),
								jen.Index().String().Values(
									jen.Qual("net/http", "MethodDelete"),
									jen.Qual("net/http", "MethodGet"),
									jen.Qual("net/http", "MethodPost"),
								),
							),
							jen.Return(),
						),
						jen.Id("actual").Call(jen.Id("w"), jen.Id("r")),
					),
				)),
			)
		stmt.Line()
	})
}

func sdkServerSessionErrorSection() codegen.Section {
	return codegen.NewJenniferSection("mcp-sdk-server-session-errors", func(stmt *jen.Statement) {
		stmt.Func().Id("writeSDKSessionError").
			Params(jen.Id("w").Qual("net/http", "ResponseWriter"), jen.Id("err").Error()).
			Block(
				jen.If(jen.Qual("errors", "Is").Call(jen.Id("err"), jen.Id("errInvalidSessionID"))).Block(
					jen.Qual("net/http", "Error").Call(jen.Id("w"), jen.Id("err").Dot("Error").Call(), jen.Qual("net/http", "StatusNotFound")),
					jen.Return(),
				),
				jen.Qual("net/http", "Error").Call(jen.Id("w"), jen.Id("err").Dot("Error").Call(), jen.Qual("net/http", "StatusForbidden")),
			)
		stmt.Line()
	})
}

func sdkServerRegistrationSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-sdk-server-registration", func(stmt *jen.Statement) {
		emitSDKRegisterTools(stmt, data)
		emitSDKRegisterResources(stmt, data)
		emitSDKRegisterPrompts(stmt, data)
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
				jen.Id("sdkTool").Op(":=").Op("&").Id("mcpsdk").Dot("Tool").Values(jen.Dict{
					jen.Id("Name"):         jen.Id("tool").Dot("Name"),
					jen.Id("Title"):        jen.Id("derefString").Call(jen.Id("tool").Dot("Title")),
					jen.Id("Description"):  jen.Id("derefString").Call(jen.Id("tool").Dot("Description")),
					jen.Id("InputSchema"):  jen.Id("tool").Dot("InputSchema"),
					jen.Id("OutputSchema"): jen.Id("tool").Dot("OutputSchema"),
					jen.Id("Annotations"):  jen.Id("annotations"),
					jen.Id("Meta"):         jen.Id("sdkMeta").Call(jen.Id("tool").Dot("Meta")),
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

func emitSDKRegisterTools(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Id("registerSDKTools").
		Params(
			jen.Id("server").Op("*").Id("mcpsdk").Dot("Server"),
			jen.Id("adapter").Op("*").Id("MCPAdapter"),
			jen.Id("requestContext").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Qual("net/http", "Request")).Qual("context", "Context"),
		).
		Error().
		BlockFunc(func(g *jen.Group) {
			g.If(jen.Id("adapter").Dot("toolSearchEnabled").Call()).Block(
				jen.Id("tools").Op(":=").Id("adapter").Dot("toolSearchSyntheticTools").Call(),
				jen.Id("tools").Op("=").Append(jen.Id("tools"), jen.Id("adapter").Dot("visibleToolCatalog").Call(jen.Id("adapter").Dot("generatedToolCatalog").Call()).Op("...")),
				jen.For(jen.List(jen.Id("_"), jen.Id("tool")).Op(":=").Range().Id("tools")).Block(
					jen.List(jen.Id("sdkTool"), jen.Id("err")).Op(":=").Id("sdkToolFromToolInfo").Call(jen.Id("tool")),
					jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Id("err"))),
					jen.Id("server").Dot("AddTool").Call(
						jen.Id("sdkTool"),
						jen.Id("adapter").Dot("sdkToolHandler").Call(jen.Id("requestContext")),
					),
				),
				jen.Return(jen.Nil()),
			)
			for _, tool := range data.Tools {
				if tool.AnnotationsJSON != "" {
					name := "annotations" + codegen.Goify(tool.Name, true)
					g.List(jen.Id(name), jen.Id("err")).Op(":=").Id("sdkToolAnnotations").Call(
						jen.Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Lit(tool.AnnotationsJSON))),
					)
					g.If(jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("tool %q annotations: %w"), jen.Lit(tool.Name), jen.Id("err"))),
					)
				}
				dict := jen.Dict{
					jen.Id("Name"):        jen.Lit(tool.Name),
					jen.Id("Title"):       jen.Lit(tool.Title),
					jen.Id("Description"): jen.Lit(tool.Description),
					jen.Id("InputSchema"): sdkToolSchemaValue(tool, true),
				}
				if outputSchema := sdkToolSchemaValue(tool, false); outputSchema != nil {
					dict[jen.Id("OutputSchema")] = outputSchema
				}
				if tool.MetaJSON != "" {
					dict[jen.Id("Meta")] = jen.Id("sdkMeta").Call(jen.Id("jsontext").Dot("Value").Call(jen.Index().Byte().Call(jen.Lit(tool.MetaJSON))))
				}
				if icons := sdkIconSliceValue(tool.Icons); icons != nil {
					dict[jen.Id("Icons")] = icons
				}
				if tool.AnnotationsJSON != "" {
					dict[jen.Id("Annotations")] = jen.Id("annotations" + codegen.Goify(tool.Name, true))
				}
				g.Id("server").Dot("AddTool").Call(
					jen.Op("&").Id("mcpsdk").Dot("Tool").Values(dict),
					jen.Id("adapter").Dot("sdkToolHandler").Call(jen.Id("requestContext")),
				)
			}
			g.Return(jen.Nil())
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

func emitSDKRegisterResources(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Id("registerSDKResources").
		Params(
			jen.Id("server").Op("*").Id("mcpsdk").Dot("Server"),
			jen.Id("adapter").Op("*").Id("MCPAdapter"),
			jen.Id("requestContext").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Qual("net/http", "Request")).Qual("context", "Context"),
		).
		Error().
		BlockFunc(func(g *jen.Group) {
			if len(data.Resources) == 0 && len(data.SkillDirectories) == 0 {
				g.Return(jen.Nil())
				return
			}
			for _, resource := range data.Resources {
				dict := jen.Dict{
					jen.Id("Name"):        jen.Lit(resource.Name),
					jen.Id("URI"):         jen.Lit(resource.URI),
					jen.Id("Description"): jen.Lit(resource.Description),
					jen.Id("MIMEType"):    jen.Lit(resource.MimeType),
				}
				if icons := sdkIconSliceValue(resource.Icons); icons != nil {
					dict[jen.Id("Icons")] = icons
				}
				g.Id("server").Dot("AddResource").Call(
					jen.Op("&").Id("mcpsdk").Dot("Resource").Values(dict),
					jen.Id("adapter").Dot("sdkResourceHandler").Call(jen.Id("requestContext")),
				)
			}
			if len(data.SkillDirectories) > 0 {
				g.List(jen.Id("skillResources"), jen.Err()).Op(":=").Id("mcpskills").Dot("List").Call(jen.Qual("context", "Background").Call(), jen.Id("skillSources").Call())
				g.If(jen.Err().Op("!=").Nil()).Block(
					jen.Return(jen.Err()),
				)
				g.For(jen.List(jen.Id("_"), jen.Id("resource")).Op(":=").Range().Id("skillResources")).Block(
					jen.Id("server").Dot("AddResource").Call(
						jen.Op("&").Id("mcpsdk").Dot("Resource").Values(jen.Dict{
							jen.Id("Name"):        jen.Id("resource").Dot("Name"),
							jen.Id("URI"):         jen.Id("resource").Dot("URI"),
							jen.Id("Description"): jen.Id("resource").Dot("Description"),
							jen.Id("MIMEType"):    jen.Id("resource").Dot("MimeType"),
							jen.Id("Meta"):        jen.Id("sdkMeta").Call(jen.Id("mcpskills").Dot("MetadataMeta").Call(jen.Id("resource").Dot("Metadata"))),
						}),
						jen.Id("adapter").Dot("sdkResourceHandler").Call(jen.Id("requestContext")),
					),
				)
			}
			g.Return(jen.Nil())
		})
	stmt.Line()
}

func emitSDKRegisterPrompts(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Id("registerSDKPrompts").
		Params(
			jen.Id("server").Op("*").Id("mcpsdk").Dot("Server"),
			jen.Id("adapter").Op("*").Id("MCPAdapter"),
			jen.Id("requestContext").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Qual("net/http", "Request")).Qual("context", "Context"),
		).
		Error().
		BlockFunc(func(g *jen.Group) {
			if len(data.StaticPrompts) == 0 && len(data.DynamicPrompts) == 0 {
				g.Return(jen.Nil())
				return
			}
			for _, prompt := range data.StaticPrompts {
				dict := jen.Dict{
					jen.Id("Name"):        jen.Lit(prompt.Name),
					jen.Id("Description"): jen.Lit(prompt.Description),
				}
				if icons := sdkIconSliceValue(prompt.Icons); icons != nil {
					dict[jen.Id("Icons")] = icons
				}
				g.Id("server").Dot("AddPrompt").Call(
					jen.Op("&").Id("mcpsdk").Dot("Prompt").Values(dict),
					jen.Id("adapter").Dot("sdkPromptHandler").Call(jen.Id("requestContext")),
				)
			}
			for _, prompt := range data.DynamicPrompts {
				dict := jen.Dict{
					jen.Id("Name"):        jen.Lit(prompt.Name),
					jen.Id("Description"): jen.Lit(prompt.Description),
					jen.Id("Arguments"):   sdkPromptArgumentsValue(prompt.Arguments),
				}
				if icons := sdkIconSliceValue(prompt.Icons); icons != nil {
					dict[jen.Id("Icons")] = icons
				}
				g.Id("server").Dot("AddPrompt").Call(
					jen.Op("&").Id("mcpsdk").Dot("Prompt").Values(dict),
					jen.Id("adapter").Dot("sdkPromptHandler").Call(jen.Id("requestContext")),
				)
			}
			g.Return(jen.Nil())
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

func sdkServerHandlerSection(data *AdapterData) codegen.Section {
	return codegen.NewJenniferSection("mcp-sdk-server-handlers", func(stmt *jen.Statement) {
		stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
			Id("sdkToolHandler").
			Params(jen.Id("requestContext").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Qual("net/http", "Request")).Qual("context", "Context")).
			Id("mcpsdk").Dot("ToolHandler").
			Block(
				jen.Return(jen.Func().
					Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("req").Op("*").Id("mcpsdk").Dot("CallToolRequest")).
					Params(jen.Op("*").Id("mcpsdk").Dot("CallToolResult"), jen.Error()).
					Block(
						jen.Id("payload").Op(":=").Op("&").Id("ToolsCallPayload").Values(),
						jen.Var().Id("inputResponses").Id("mcpsdk").Dot("InputResponseMap"),
						jen.Id("requestState").Op(":=").Lit(""),
						jen.If(jen.Id("req").Op("!=").Nil().Op("&&").Id("req").Dot("Params").Op("!=").Nil()).Block(
							jen.Id("payload").Dot("Name").Op("=").Id("req").Dot("Params").Dot("Name"),
							jen.Id("payload").Dot("Arguments").Op("=").Id("mcpJSONFromRaw").Call(jen.Id("req").Dot("Params").Dot("Arguments")),
							jen.Id("inputResponses").Op("=").Id("req").Dot("Params").Dot("InputResponses"),
							jen.Id("requestState").Op("=").Id("req").Dot("Params").Dot("RequestState"),
						),
						jen.Id("ctx").Op("=").Id("a").Dot("sdkRequestContext").Call(jen.Id("ctx"), jen.Id("req").Dot("GetSession").Call(), jen.Id("req").Dot("GetExtra").Call(), jen.Id("requestContext"), jen.Id("inputResponses"), jen.Id("requestState"), jen.Lit("tools/call"), jen.Id("payload")),
						jen.If(jen.Id("req").Op("!=").Nil().Op("&&").Id("req").Dot("Params").Op("!=").Nil()).Block(
							jen.Id("ctx").Op("=").Id("mcpruntime").Dot("WithProgressToken").Call(jen.Id("ctx"), jen.Id("req").Dot("Params").Dot("GetProgressToken").Call()),
						),
						jen.List(jen.Id("result"), jen.Id("err")).Op(":=").Id("a").Dot("ToolsCall").Call(jen.Id("ctx"), jen.Id("payload")),
						jen.If(jen.Id("err").Op("!=").Nil()).Block(
							jen.If(jen.List(jen.Id("requests"), jen.Id("state"), jen.Id("ok")).Op(":=").Id("sdkclient").Dot("InputRequired").Call(jen.Id("err")), jen.Id("ok")).Block(
								jen.Return(jen.Op("&").Id("mcpsdk").Dot("CallToolResult").Values(jen.Dict{
									jen.Id("InputRequests"): jen.Id("requests"),
									jen.Id("RequestState"):  jen.Id("state"),
								}), jen.Nil()),
							),
							jen.Return(jen.Nil(), jen.Id("err")),
						),
						jen.Return(jen.Id("sdkCallToolResult").Call(jen.Id("result"))),
					)),
			)
		stmt.Line()
		if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
			stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
				Id("sdkPromptHandler").
				Params(jen.Id("requestContext").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Qual("net/http", "Request")).Qual("context", "Context")).
				Id("mcpsdk").Dot("PromptHandler").
				Block(
					jen.Return(jen.Func().
						Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("req").Op("*").Id("mcpsdk").Dot("GetPromptRequest")).
						Params(jen.Op("*").Id("mcpsdk").Dot("GetPromptResult"), jen.Error()).
						Block(
							jen.Id("payload").Op(":=").Op("&").Id("PromptsGetPayload").Values(),
							jen.Var().Id("inputResponses").Id("mcpsdk").Dot("InputResponseMap"),
							jen.Id("requestState").Op(":=").Lit(""),
							jen.If(jen.Id("req").Op("!=").Nil().Op("&&").Id("req").Dot("Params").Op("!=").Nil()).Block(
								jen.Id("payload").Dot("Name").Op("=").Id("req").Dot("Params").Dot("Name"),
								jen.Id("inputResponses").Op("=").Id("req").Dot("Params").Dot("InputResponses"),
								jen.Id("requestState").Op("=").Id("req").Dot("Params").Dot("RequestState"),
								jen.If(jen.Id("req").Dot("Params").Dot("Arguments").Op("!=").Nil()).Block(
									jen.List(jen.Id("args"), jen.Id("err")).Op(":=").Id("json").Dot("Marshal").Call(jen.Id("req").Dot("Params").Dot("Arguments")),
									jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
									jen.Id("payload").Dot("Arguments").Op("=").Id("mcpJSONFromRaw").Call(jen.Id("args")),
								),
							),
							jen.Id("ctx").Op("=").Id("a").Dot("sdkRequestContext").Call(jen.Id("ctx"), jen.Id("req").Dot("GetSession").Call(), jen.Id("req").Dot("GetExtra").Call(), jen.Id("requestContext"), jen.Id("inputResponses"), jen.Id("requestState"), jen.Lit("prompts/get"), jen.Id("payload")),
							jen.List(jen.Id("result"), jen.Id("err")).Op(":=").Id("a").Dot("PromptsGet").Call(jen.Id("ctx"), jen.Id("payload")),
							jen.If(jen.Id("err").Op("!=").Nil()).Block(
								jen.If(jen.List(jen.Id("requests"), jen.Id("state"), jen.Id("ok")).Op(":=").Id("sdkclient").Dot("InputRequired").Call(jen.Id("err")), jen.Id("ok")).Block(
									jen.Return(jen.Op("&").Id("mcpsdk").Dot("GetPromptResult").Values(jen.Dict{
										jen.Id("InputRequests"): jen.Id("requests"),
										jen.Id("RequestState"):  jen.Id("state"),
									}), jen.Nil()),
								),
								jen.Return(jen.Nil(), jen.Id("err")),
							),
							jen.Return(jen.Id("sdkGetPromptResult").Call(jen.Id("result"))),
						)),
				)
			stmt.Line()
			stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
				Id("sdkCompletionHandler").
				Params(jen.Id("requestContext").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Qual("net/http", "Request")).Qual("context", "Context")).
				Func().
				Params(jen.Qual("context", "Context"), jen.Op("*").Id("mcpsdk").Dot("CompleteRequest")).
				Params(jen.Op("*").Id("mcpsdk").Dot("CompleteResult"), jen.Error()).
				Block(
					jen.Return(jen.Func().
						Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("req").Op("*").Id("mcpsdk").Dot("CompleteRequest")).
						Params(jen.Op("*").Id("mcpsdk").Dot("CompleteResult"), jen.Error()).
						BlockFunc(func(g *jen.Group) {
							g.If(jen.Id("req").Op("!=").Nil()).Block(
								jen.Id("ctx").Op("=").Id("a").Dot("sdkRequestContext").Call(jen.Id("ctx"), jen.Id("req").Dot("GetSession").Call(), jen.Id("req").Dot("GetExtra").Call(), jen.Id("requestContext"), jen.Nil(), jen.Lit(""), jen.Lit("completion/complete"), jen.Id("req").Dot("Params")),
							)
							g.If(jen.Id("req").Op("==").Nil().Op("||").Id("req").Dot("Params").Op("==").Nil().Op("||").Id("req").Dot("Params").Dot("Ref").Op("==").Nil()).Block(
								jen.Return(jen.Id("sdkCompleteValues").Call(jen.Nil(), jen.Lit(0), jen.False()), jen.Nil()),
							)
							g.If(jen.Id("req").Dot("Params").Dot("Ref").Dot("Type").Op("!=").Lit("ref/prompt")).Block(
								jen.Return(jen.Id("sdkCompleteValues").Call(jen.Nil(), jen.Lit(0), jen.False()), jen.Nil()),
							)
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
												argg.Case(jen.Lit(arg.Name)).Block(
													jen.Return(
														jen.Id("sdkFilteredCompletion").Call(
															jen.Id("req").Dot("Params").Dot("Argument").Dot("Value"),
															jen.Index().String().Values(values...),
														),
														jen.Nil(),
													),
												)
											}
											argg.Default().Block(
												jen.Return(jen.Id("sdkCompleteValues").Call(jen.Nil(), jen.Lit(0), jen.False()), jen.Nil()),
											)
										})
									})
								}
								sg.Default().Block(
									jen.Return(jen.Id("sdkCompleteValues").Call(jen.Nil(), jen.Lit(0), jen.False()), jen.Nil()),
								)
							})
						})),
				)
			stmt.Line()
		}
		if len(data.Resources) > 0 || len(data.SkillDirectories) > 0 {
			stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
				Id("sdkResourceHandler").
				Params(jen.Id("requestContext").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Qual("net/http", "Request")).Qual("context", "Context")).
				Id("mcpsdk").Dot("ResourceHandler").
				Block(
					jen.Return(jen.Func().
						Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("req").Op("*").Id("mcpsdk").Dot("ReadResourceRequest")).
						Params(jen.Op("*").Id("mcpsdk").Dot("ReadResourceResult"), jen.Error()).
						Block(
							jen.Id("payload").Op(":=").Op("&").Id("ResourcesReadPayload").Values(),
							jen.Var().Id("inputResponses").Id("mcpsdk").Dot("InputResponseMap"),
							jen.Id("requestState").Op(":=").Lit(""),
							jen.If(jen.Id("req").Op("!=").Nil().Op("&&").Id("req").Dot("Params").Op("!=").Nil()).Block(
								jen.Id("payload").Dot("URI").Op("=").Id("req").Dot("Params").Dot("URI"),
								jen.Id("inputResponses").Op("=").Id("req").Dot("Params").Dot("InputResponses"),
								jen.Id("requestState").Op("=").Id("req").Dot("Params").Dot("RequestState"),
							),
							jen.Id("ctx").Op("=").Id("a").Dot("sdkRequestContext").Call(jen.Id("ctx"), jen.Id("req").Dot("GetSession").Call(), jen.Id("req").Dot("GetExtra").Call(), jen.Id("requestContext"), jen.Id("inputResponses"), jen.Id("requestState"), jen.Lit("resources/read"), jen.Id("payload")),
							jen.List(jen.Id("result"), jen.Id("err")).Op(":=").Id("a").Dot("ResourcesRead").Call(jen.Id("ctx"), jen.Id("payload")),
							jen.If(jen.Id("err").Op("!=").Nil()).Block(
								jen.If(jen.List(jen.Id("requests"), jen.Id("state"), jen.Id("ok")).Op(":=").Id("sdkclient").Dot("InputRequired").Call(jen.Id("err")), jen.Id("ok")).Block(
									jen.Return(jen.Op("&").Id("mcpsdk").Dot("ReadResourceResult").Values(jen.Dict{
										jen.Id("InputRequests"): jen.Id("requests"),
										jen.Id("RequestState"):  jen.Id("state"),
									}), jen.Nil()),
								),
								jen.Return(jen.Nil(), jen.Id("err")),
							),
							jen.Return(jen.Id("sdkReadResourceResult").Call(jen.Id("result"))),
						)),
				)
			stmt.Line()
		}

		stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
			Id("sdkRequestContext").
			Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("session").Id("mcpsdk").Dot("Session"),
				jen.Id("extra").Op("*").Id("mcpsdk").Dot("RequestExtra"),
				jen.Id("requestContext").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Qual("net/http", "Request")).Qual("context", "Context"),
				jen.Id("inputResponses").Id("mcpsdk").Dot("InputResponseMap"),
				jen.Id("requestState").String(),
				jen.Id("requestMethod").String(),
				jen.Id("requestParams").Any(),
			).
			Qual("context", "Context").
			Block(
				jen.If(jen.Id("requestContext").Op("!=").Nil()).Block(
					jen.Id("ctx").Op("=").Id("requestContext").Call(jen.Id("ctx"), jen.Id("sdkSyntheticHTTPRequest").Call(jen.Id("ctx"), jen.Id("extra"))),
				),
				jen.If(jen.Id("session").Op("==").Nil()).Block(
					jen.Id("a").Dot("markInitializedSession").Call(jen.Lit("")),
					jen.Return(jen.Id("ctx")),
				),
				jen.Id("ctx").Op("=").Id("sdkContextWithClientFeatures").Call(jen.Id("ctx"), jen.Id("session"), jen.Id("inputResponses"), jen.Id("requestState"), jen.Id("a").Dot("requestStateKey"), jen.Id("requestMethod"), jen.Id("requestParams")),
				jen.Id("sessionID").Op(":=").Id("session").Dot("ID").Call(),
				jen.If(jen.Id("sessionID").Op("==").Lit("")).Block(
					jen.Id("a").Dot("markInitializedSession").Call(jen.Lit("")),
					jen.Return(jen.Id("ctx")),
				),
				jen.Id("a").Dot("markInitializedSession").Call(jen.Id("sessionID")),
				jen.Return(jen.Id("mcpruntime").Dot("WithSessionID").Call(jen.Id("ctx"), jen.Id("sessionID"))),
			)
		stmt.Line()

		stmt.Func().Id("sdkContextWithClientFeatures").
			Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("session").Id("mcpsdk").Dot("Session"),
				jen.Id("inputResponses").Id("mcpsdk").Dot("InputResponseMap"),
				jen.Id("requestState").String(),
				jen.Id("requestStateKey").Index().Byte(),
				jen.Id("requestMethod").String(),
				jen.Id("requestParams").Any(),
			).
			Qual("context", "Context").
			Block(
				jen.Id("serverSession").Op(",").Id("ok").Op(":=").Id("session").Assert(jen.Op("*").Id("mcpsdk").Dot("ServerSession")),
				jen.If(jen.Op("!").Id("ok").Op("||").Id("serverSession").Op("==").Nil()).Block(
					jen.Return(jen.Id("ctx")),
				),
				jen.Return(jen.Id("sdkclient").Dot("WithClientFeatures").Call(
					jen.Id("ctx"),
					jen.Id("serverSession"),
					jen.Id("sdkclient").Dot("ClientFeaturesOptions").Values(jen.Dict{
						jen.Id("InputResponses"):  jen.Id("inputResponses"),
						jen.Id("RequestState"):    jen.Id("requestState"),
						jen.Id("RequestStateKey"): jen.Id("requestStateKey"),
						jen.Id("RequestMethod"):   jen.Id("requestMethod"),
						jen.Id("RequestParams"):   jen.Id("requestParams"),
					}),
				)),
			)
		stmt.Line()

		stmt.Func().Id("sdkSyntheticHTTPRequest").
			Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("extra").Op("*").Id("mcpsdk").Dot("RequestExtra")).
			Op("*").Qual("net/http", "Request").
			Block(
				jen.Id("req").Op(":=").Op("&").Qual("net/http", "Request").Values(jen.Dict{
					jen.Id("Method"): jen.Qual("net/http", "MethodPost"),
					jen.Id("Header"): jen.Make(jen.Qual("net/http", "Header")),
					jen.Id("URL"):    jen.Op("&").Qual("net/url", "URL").Values(jen.Dict{jen.Id("Path"): jen.Lit("/mcp")}),
				}),
				jen.For(jen.List(jen.Id("key"), jen.Id("values")).Op(":=").Range().Id("mcpruntime").Dot("RequestHeadersFromContext").Call(jen.Id("ctx"))).Block(
					jen.Id("req").Dot("Header").Dot("Del").Call(jen.Id("key")),
					jen.For(jen.List(jen.Id("_"), jen.Id("value")).Op(":=").Range().Id("values")).Block(
						jen.Id("req").Dot("Header").Dot("Add").Call(jen.Id("key"), jen.Id("value")),
					),
				),
				jen.If(jen.Id("extra").Op("!=").Nil().Op("&&").Id("extra").Dot("Header").Op("!=").Nil()).Block(
					jen.For(jen.List(jen.Id("key"), jen.Id("values")).Op(":=").Range().Id("extra").Dot("Header")).Block(
						jen.Id("req").Dot("Header").Dot("Del").Call(jen.Id("key")),
						jen.For(jen.List(jen.Id("_"), jen.Id("value")).Op(":=").Range().Id("values")).Block(
							jen.Id("req").Dot("Header").Dot("Add").Call(jen.Id("key"), jen.Id("value")),
						),
					),
				),
				jen.Return(jen.Id("req")),
			)
		stmt.Line()
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
		Params(jen.Id("result").Op("*").Id("ResourcesReadResult")).
		Params(jen.Op("*").Id("mcpsdk").Dot("ReadResourceResult"), jen.Error()).
		Block(
			jen.If(jen.Id("result").Op("==").Nil()).Block(
				jen.Return(jen.Op("&").Id("mcpsdk").Dot("ReadResourceResult").Values(jen.Dict{
					jen.Id("Contents"): jen.Index().Op("*").Id("mcpsdk").Dot("ResourceContents").Values(),
				}), jen.Nil()),
			),
			jen.Id("contents").Op(":=").Make(jen.Index().Op("*").Id("mcpsdk").Dot("ResourceContents"), jen.Lit(0), jen.Len(jen.Id("result").Dot("Contents"))),
			jen.For(jen.List(jen.Id("_"), jen.Id("content")).Op(":=").Range().Id("result").Dot("Contents")).Block(
				jen.List(jen.Id("converted"), jen.Id("err")).Op(":=").Id("sdkReadResourceContent").Call(jen.Id("content")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
				jen.Id("contents").Op("=").Append(jen.Id("contents"), jen.Id("converted")),
			),
			jen.Return(jen.Op("&").Id("mcpsdk").Dot("ReadResourceResult").Values(jen.Dict{
				jen.Id("Contents"): jen.Id("contents"),
			}), jen.Nil()),
		)
	stmt.Line()
	stmt.Func().Id("sdkReadResourceContent").
		Params(jen.Id("item").Op("*").Id("ResourceContent")).
		Params(jen.Op("*").Id("mcpsdk").Dot("ResourceContents"), jen.Error()).
		Block(
			jen.If(jen.Id("item").Op("==").Nil()).Block(
				jen.Return(jen.Op("&").Id("mcpsdk").Dot("ResourceContents").Values(), jen.Nil()),
			),
			jen.Id("resource").Op(":=").Op("&").Id("mcpsdk").Dot("ResourceContents").Values(jen.Dict{
				jen.Id("URI"):      jen.Id("item").Dot("URI"),
				jen.Id("MIMEType"): jen.Id("derefString").Call(jen.Id("item").Dot("MimeType")),
				jen.Id("Text"):     jen.Id("derefString").Call(jen.Id("item").Dot("Text")),
				jen.Id("Meta"):     jen.Id("sdkMeta").Call(jen.Id("mcpJSONAny").Call(jen.Id("item").Dot("Meta"))),
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

func emitSDKContentConversions(stmt *jen.Statement) {
	stmt.Func().Id("sdkContentFromItem").
		Params(jen.Id("item").Op("*").Id("ContentItem")).
		Params(jen.Id("mcpsdk").Dot("Content"), jen.Error()).
		Block(
			jen.If(jen.Id("item").Op("==").Nil()).Block(
				jen.Return(jen.Op("&").Id("mcpsdk").Dot("TextContent").Values(), jen.Nil()),
			),
			jen.Switch(jen.Id("item").Dot("Type")).Block(
				jen.Case(jen.Lit("text")).Block(
					jen.Return(jen.Op("&").Id("mcpsdk").Dot("TextContent").Values(jen.Dict{jen.Id("Text"): jen.Id("derefString").Call(jen.Id("item").Dot("Text"))}), jen.Nil()),
				),
				jen.Case(jen.Lit("image")).Block(
					jen.List(jen.Id("data"), jen.Id("err")).Op(":=").Id("sdkDecodeBase64").Call(jen.Id("item").Dot("Data")),
					jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
					jen.Return(jen.Op("&").Id("mcpsdk").Dot("ImageContent").Values(jen.Dict{
						jen.Id("Data"):     jen.Id("data"),
						jen.Id("MIMEType"): jen.Id("derefString").Call(jen.Id("item").Dot("MimeType")),
					}), jen.Nil()),
				),
				jen.Case(jen.Lit("audio")).Block(
					jen.List(jen.Id("data"), jen.Id("err")).Op(":=").Id("sdkDecodeBase64").Call(jen.Id("item").Dot("Data")),
					jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
					jen.Return(jen.Op("&").Id("mcpsdk").Dot("AudioContent").Values(jen.Dict{
						jen.Id("Data"):     jen.Id("data"),
						jen.Id("MIMEType"): jen.Id("derefString").Call(jen.Id("item").Dot("MimeType")),
					}), jen.Nil()),
				),
				jen.Case(jen.Lit("resource")).Block(
					jen.List(jen.Id("resource"), jen.Id("err")).Op(":=").Id("sdkResourceContents").Call(jen.Id("item")),
					jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
					jen.Return(jen.Op("&").Id("mcpsdk").Dot("EmbeddedResource").Values(jen.Dict{jen.Id("Resource"): jen.Id("resource")}), jen.Nil()),
				),
				jen.Default().Block(
					jen.If(jen.Id("item").Dot("URI").Op("!=").Nil()).Block(
						jen.List(jen.Id("resource"), jen.Id("err")).Op(":=").Id("sdkResourceContents").Call(jen.Id("item")),
						jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
						jen.Return(jen.Op("&").Id("mcpsdk").Dot("EmbeddedResource").Values(jen.Dict{jen.Id("Resource"): jen.Id("resource")}), jen.Nil()),
					),
					jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("unsupported MCP content type %q"), jen.Id("item").Dot("Type"))),
				),
			),
		)
	stmt.Line()

	stmt.Func().Id("sdkResourceContents").
		Params(jen.Id("item").Op("*").Id("ContentItem")).
		Params(jen.Op("*").Id("mcpsdk").Dot("ResourceContents"), jen.Error()).
		Block(
			jen.Id("resource").Op(":=").Op("&").Id("mcpsdk").Dot("ResourceContents").Values(jen.Dict{
				jen.Id("URI"):      jen.Id("derefString").Call(jen.Id("item").Dot("URI")),
				jen.Id("MIMEType"): jen.Id("derefString").Call(jen.Id("item").Dot("MimeType")),
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
		Id("mcpsdk").Dot("Meta").
		Block(
			jen.Switch(jen.Id("typed").Op(":=").Id("value").Assert(jen.Type())).Block(
				jen.Case(jen.Nil()).Block(
					jen.Return(jen.Nil()),
				),
				jen.Case(jen.Id("mcpsdk").Dot("Meta")).Block(
					jen.Return(jen.Id("typed")),
				),
				jen.Case(jen.Map(jen.String()).Any()).Block(
					jen.Return(jen.Id("mcpsdk").Dot("Meta").Call(jen.Id("typed"))),
				),
				jen.Case(jen.Id("jsontext").Dot("Value")).Block(
					jen.Var().Id("meta").Map(jen.String()).Any(),
					jen.If(jen.Id("err").Op(":=").Id("json").Dot("Unmarshal").Call(jen.Id("typed"), jen.Op("&").Id("meta")), jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Nil()),
					),
					jen.Return(jen.Id("mcpsdk").Dot("Meta").Call(jen.Id("meta"))),
				),
				jen.Case(jen.Index().Byte()).Block(
					jen.Var().Id("meta").Map(jen.String()).Any(),
					jen.If(jen.Id("err").Op(":=").Id("json").Dot("Unmarshal").Call(jen.Id("typed"), jen.Op("&").Id("meta")), jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Nil()),
					),
					jen.Return(jen.Id("mcpsdk").Dot("Meta").Call(jen.Id("meta"))),
				),
				jen.Default().Block(
					jen.Return(jen.Nil()),
				),
			),
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
