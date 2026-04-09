package codegen

import (
	"path/filepath"

	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/expr"
	"github.com/dave/jennifer/jen"
)

func buildMCPSDKServerFile(genpkg string, svc *expr.ServiceExpr, data *AdapterData, svcName, pkgName string) *codegen.File {
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
	sections := []codegen.Section{
		codegen.Header("SDK-backed MCP server for "+svc.Name+" service", pkgName, sdkServerImports),
		sdkServerTypesSection(data),
		sdkServerConstructorSection(data),
		sdkServerHTTPSection(),
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
	return codegen.MustJenniferSection("mcp-sdk-server-types", func(stmt *jen.Statement) {
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
		)
		stmt.Line()
		stmt.Type().Id("sdkToolCallCollector").Struct(
			jen.Id("parts").Index().Op("*").Id("ToolsCallResult"),
			jen.Id("final").Op("*").Id("ToolsCallResult"),
			jen.Id("streamErr").Error(),
		)
		stmt.Line()
	})
}

func sdkServerConstructorSection(data *AdapterData) codegen.Section {
	return codegen.MustJenniferSection("mcp-sdk-server-constructor", func(stmt *jen.Statement) {
		stmt.Func().Id("NewSDKServer").
			Params(
				jen.Id("service").Id(data.Package).Dot("Service"),
				jen.Id("opts").Op("*").Id("SDKServerOptions"),
			).
			Params(jen.Op("*").Id("SDKServer"), jen.Error()).
			BlockFunc(func(g *jen.Group) {
				g.Var().Id("adapterOpts").Op("*").Id("MCPAdapterOptions")
				g.Var().Id("requestContext").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Qual("net/http", "Request")).Qual("context", "Context")
				if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
					g.Var().Id("promptProvider").Id("PromptProvider")
				}
				g.Var().Id("serverOpts").Op("*").Id("mcpsdk").Dot("ServerOptions")
				g.Var().Id("streamableOpts").Op("*").Id("mcpsdk").Dot("StreamableHTTPOptions")
				g.If(jen.Id("opts").Op("!=").Nil()).BlockFunc(func(ifg *jen.Group) {
					ifg.Id("adapterOpts").Op("=").Id("opts").Dot("Adapter")
					ifg.Id("requestContext").Op("=").Id("opts").Dot("RequestContext")
					if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
						ifg.Id("promptProvider").Op("=").Id("opts").Dot("PromptProvider")
					}
					ifg.Id("serverOpts").Op("=").Id("opts").Dot("Server")
					ifg.Id("streamableOpts").Op("=").Id("opts").Dot("StreamableHTTP")
				})
				if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
					g.Id("adapter").Op(":=").Id("NewMCPAdapter").Call(jen.Id("service"), jen.Id("promptProvider"), jen.Id("adapterOpts"))
				} else {
					g.Id("adapter").Op(":=").Id("NewMCPAdapter").Call(jen.Id("service"), jen.Id("adapterOpts"))
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
				g.Return(
					jen.Op("&").Id("SDKServer").Values(jen.Dict{
						jen.Id("Handler"): jen.Id("newSDKHandler").Call(jen.Id("server"), jen.Id("adapter"), jen.Id("requestContext"), jen.Id("streamableOpts")),
						jen.Id("Adapter"): jen.Id("adapter"),
						jen.Id("Server"):  jen.Id("server"),
					}),
					jen.Nil(),
				)
			})
		stmt.Line()

		stmt.Func().Params(jen.Id("w").Op("*").Id("sdkResponseObserver")).
			Id("WriteHeader").
			Params(jen.Id("statusCode").Int()).
			Block(
				jen.Id("w").Dot("statusCode").Op("=").Id("statusCode"),
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
				jen.Return(jen.Id("w").Dot("ResponseWriter").Dot("Write").Call(jen.Id("data"))),
			)
		stmt.Line()
	})
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
	return codegen.MustJenniferSection("mcp-sdk-server-http", func(stmt *jen.Statement) {
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
					jen.Id("streamableOpts"),
				),
				jen.Return(
					jen.Qual("net/http", "HandlerFunc").Call(
						jen.Func().Params(jen.Id("w").Qual("net/http", "ResponseWriter"), jen.Id("r").Op("*").Qual("net/http", "Request")).Block(
							jen.Id("r").Op("=").Id("r").Dot("WithContext").Call(jen.Id("mcpruntime").Dot("WithRequestHeaders").Call(jen.Id("r").Dot("Context").Call(), jen.Id("r").Dot("Header"))),
							jen.If(jen.Id("requestContext").Op("!=").Nil()).Block(
								jen.Id("r").Op("=").Id("r").Dot("WithContext").Call(jen.Id("requestContext").Call(jen.Id("r").Dot("Context").Call(), jen.Id("r"))),
							),
							jen.If(jen.Id("r").Dot("Method").Op("==").Qual("net/http", "MethodGet")).Block(
								jen.Id("serveSDKEventsStream").Call(jen.Id("server"), jen.Id("adapter"), jen.Id("w"), jen.Id("r")),
								jen.Return(),
							),
							jen.Id("observer").Op(":=").Op("&").Id("sdkResponseObserver").Values(jen.Dict{jen.Id("ResponseWriter"): jen.Id("w")}),
							jen.Id("base").Dot("ServeHTTP").Call(jen.Id("observer"), jen.Id("r")),
							jen.If(jen.Id("sessionID").Op(":=").Id("observer").Dot("Header").Call().Dot("Get").Call(jen.Id("mcpruntime").Dot("HeaderKeySessionID")), jen.Id("sessionID").Op("!=").Lit("")).Block(
								jen.Id("adapter").Dot("captureSessionPrincipal").Call(jen.Id("r").Dot("Context").Call(), jen.Id("sessionID")),
							),
						),
					),
				),
			)
		stmt.Line()

		stmt.Func().Id("serveSDKEventsStream").
			Params(
				jen.Id("server").Op("*").Id("mcpsdk").Dot("Server"),
				jen.Id("adapter").Op("*").Id("MCPAdapter"),
				jen.Id("w").Qual("net/http", "ResponseWriter"),
				jen.Id("r").Op("*").Qual("net/http", "Request"),
			).
			Block(
				jen.Id("sessionID").Op(":=").Id("r").Dot("Header").Dot("Get").Call(jen.Lit("Mcp-Session-Id")),
				jen.Id("adapter").Dot("log").Call(jen.Id("r").Dot("Context").Call(), jen.Lit("events_stream_open"), jen.Map(jen.String()).Any().Values(jen.Dict{
					jen.Lit("session_id"): jen.Id("sessionID"),
					jen.Lit("has_accept"): jen.Qual("strings", "TrimSpace").Call(jen.Id("r").Dot("Header").Dot("Get").Call(jen.Lit("Accept"))).Op("!=").Lit(""),
					jen.Lit("accept"):     jen.Id("r").Dot("Header").Dot("Get").Call(jen.Lit("Accept")),
				})),
				jen.If(jen.Id("sessionID").Op("==").Lit("")).Block(
					jen.Id("adapter").Dot("log").Call(jen.Id("r").Dot("Context").Call(), jen.Lit("events_stream_rejected"), jen.Map(jen.String()).Any().Values(jen.Dict{
						jen.Lit("reason"): jen.Lit("missing_session_id"),
					})),
					jen.Qual("net/http", "Error").Call(jen.Id("w"), jen.Lit("Missing session ID"), jen.Qual("net/http", "StatusBadRequest")),
					jen.Return(),
				),
				jen.If(jen.Id("sdkSessionByID").Call(jen.Id("server"), jen.Id("sessionID")).Op("==").Nil()).Block(
					jen.Id("adapter").Dot("clearSessionPrincipal").Call(jen.Id("sessionID")),
					jen.Id("adapter").Dot("log").Call(jen.Id("r").Dot("Context").Call(), jen.Lit("events_stream_rejected"), jen.Map(jen.String()).Any().Values(jen.Dict{
						jen.Lit("session_id"): jen.Id("sessionID"),
						jen.Lit("reason"):     jen.Lit("session_not_found"),
					})),
					jen.Qual("net/http", "Error").Call(jen.Id("w"), jen.Lit("session not found"), jen.Qual("net/http", "StatusNotFound")),
					jen.Return(),
				),
				jen.If(jen.Id("err").Op(":=").Id("adapter").Dot("assertSessionPrincipal").Call(jen.Id("r").Dot("Context").Call(), jen.Id("sessionID")), jen.Id("err").Op("!=").Nil()).Block(
					jen.Id("adapter").Dot("log").Call(jen.Id("r").Dot("Context").Call(), jen.Lit("events_stream_rejected"), jen.Map(jen.String()).Any().Values(jen.Dict{
						jen.Lit("session_id"): jen.Id("sessionID"),
						jen.Lit("reason"):     jen.Lit("session_principal_mismatch"),
						jen.Lit("error"):      jen.Id("err").Dot("Error").Call(),
					})),
					jen.Qual("net/http", "Error").Call(jen.Id("w"), jen.Id("err").Dot("Error").Call(), jen.Qual("net/http", "StatusForbidden")),
					jen.Return(),
				),
				jen.Id("adapter").Dot("markInitializedSession").Call(jen.Id("sessionID")),
				jen.Id("adapter").Dot("captureSessionPrincipal").Call(jen.Id("r").Dot("Context").Call(), jen.Id("sessionID")),
				jen.List(jen.Id("sub"), jen.Id("err")).Op(":=").Id("adapter").Dot("broadcaster").Dot("Subscribe").Call(jen.Id("r").Dot("Context").Call()),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(
					jen.Qual("net/http", "Error").Call(jen.Id("w"), jen.Qual("fmt", "Sprintf").Call(jen.Lit("failed to subscribe to events: %v"), jen.Id("err")), jen.Qual("net/http", "StatusInternalServerError")),
					jen.Return(),
				),
				jen.Defer().Id("sub").Dot("Close").Call(),
				jen.Id("w").Dot("Header").Call().Dot("Set").Call(jen.Lit("Content-Type"), jen.Lit("text/event-stream")),
				jen.Id("w").Dot("Header").Call().Dot("Set").Call(jen.Lit("Cache-Control"), jen.Lit("no-cache")),
				jen.Id("w").Dot("Header").Call().Dot("Set").Call(jen.Lit("Connection"), jen.Lit("keep-alive")),
				jen.Id("w").Dot("Header").Call().Dot("Set").Call(jen.Lit("X-Accel-Buffering"), jen.Lit("no")),
				jen.List(jen.Id("flusher"), jen.Id("_")).Op(":=").Id("w").Assert(jen.Qual("net/http", "Flusher")),
				jen.Id("w").Dot("WriteHeader").Call(jen.Qual("net/http", "StatusOK")),
				jen.If(jen.Id("flusher").Op("!=").Nil()).Block(
					jen.Id("flusher").Dot("Flush").Call(),
				),
				jen.Id("adapter").Dot("log").Call(jen.Id("r").Dot("Context").Call(), jen.Lit("events_stream_connected"), jen.Map(jen.String()).Any().Values(jen.Dict{
					jen.Lit("session_id"): jen.Id("sessionID"),
					jen.Lit("flushed"):    jen.Id("flusher").Op("!=").Nil(),
				})),
				jen.Id("ticker").Op(":=").Qual("time", "NewTicker").Call(jen.Lit(250).Op("*").Qual("time", "Millisecond")),
				jen.Defer().Id("ticker").Dot("Stop").Call(),
				jen.For().Block(
					jen.Select().Block(
						jen.Case(jen.Op("<-").Id("r").Dot("Context").Call().Dot("Done").Call()).Block(
							jen.Id("adapter").Dot("log").Call(jen.Id("r").Dot("Context").Call(), jen.Lit("events_stream_closed"), jen.Map(jen.String()).Any().Values(jen.Dict{
								jen.Lit("session_id"): jen.Id("sessionID"),
								jen.Lit("reason"):     jen.Lit("request_context_done"),
							})),
							jen.Return(),
						),
						jen.Case(jen.Op("<-").Id("ticker").Dot("C")).Block(
							jen.If(jen.Id("sdkSessionByID").Call(jen.Id("server"), jen.Id("sessionID")).Op("==").Nil()).Block(
								jen.Id("adapter").Dot("clearSessionPrincipal").Call(jen.Id("sessionID")),
								jen.Id("adapter").Dot("log").Call(jen.Id("r").Dot("Context").Call(), jen.Lit("events_stream_closed"), jen.Map(jen.String()).Any().Values(jen.Dict{
									jen.Lit("session_id"): jen.Id("sessionID"),
									jen.Lit("reason"):     jen.Lit("session_not_found"),
								})),
								jen.Return(),
							),
						),
						jen.Case(jen.List(jen.Id("ev"), jen.Id("ok")).Op(":=").Op("<-").Id("sub").Dot("C").Call()).Block(
							jen.If(jen.Op("!").Id("ok")).Block(
								jen.Id("adapter").Dot("log").Call(jen.Id("r").Dot("Context").Call(), jen.Lit("events_stream_closed"), jen.Map(jen.String()).Any().Values(jen.Dict{
									jen.Lit("session_id"): jen.Id("sessionID"),
									jen.Lit("reason"):     jen.Lit("broadcaster_closed"),
								})),
								jen.Return(),
							),
							jen.List(jen.Id("res"), jen.Id("ok")).Op(":=").Id("ev").Assert(jen.Op("*").Id("EventsStreamResult")),
							jen.If(jen.Op("!").Id("ok")).Block(
								jen.Id("adapter").Dot("log").Call(jen.Id("r").Dot("Context").Call(), jen.Lit("events_stream_skipped_event"), jen.Map(jen.String()).Any().Values(jen.Dict{
									jen.Lit("session_id"): jen.Id("sessionID"),
								})),
								jen.Continue(),
							),
							jen.If(jen.Id("err").Op(":=").Id("writeSDKNotificationEvent").Call(jen.Id("w"), jen.Lit("events/stream"), jen.Id("sdkEventsStreamParams").Call(jen.Id("res"))), jen.Id("err").Op("!=").Nil()).Block(
								jen.Id("adapter").Dot("log").Call(jen.Id("r").Dot("Context").Call(), jen.Lit("events_stream_closed"), jen.Map(jen.String()).Any().Values(jen.Dict{
									jen.Lit("session_id"): jen.Id("sessionID"),
									jen.Lit("reason"):     jen.Lit("write_error"),
									jen.Lit("error"):      jen.Id("err").Dot("Error").Call(),
								})),
								jen.Return(),
							),
							jen.If(jen.Id("flusher").Op("!=").Nil()).Block(
								jen.Id("flusher").Dot("Flush").Call(),
							),
						),
					),
				),
			)
		stmt.Line()

		stmt.Func().Id("sdkSessionByID").
			Params(jen.Id("server").Op("*").Id("mcpsdk").Dot("Server"), jen.Id("sessionID").String()).
			Op("*").Id("mcpsdk").Dot("ServerSession").
			Block(
				jen.If(jen.Id("server").Op("==").Nil().Op("||").Id("sessionID").Op("==").Lit("")).Block(
					jen.Return(jen.Nil()),
				),
				jen.For(jen.Id("session").Op(":=").Range().Id("server").Dot("Sessions").Call()).Block(
					jen.If(jen.Id("session").Op("!=").Nil().Op("&&").Id("session").Dot("ID").Call().Op("==").Id("sessionID")).Block(
						jen.Return(jen.Id("session")),
					),
				),
				jen.Return(jen.Nil()),
			)
		stmt.Line()

		stmt.Func().Id("writeSDKNotificationEvent").
			Params(jen.Id("w").Qual("net/http", "ResponseWriter"), jen.Id("method").String(), jen.Id("params").Any()).
			Error().
			Block(
				jen.List(jen.Id("message"), jen.Id("err")).Op(":=").Qual("encoding/json", "Marshal").Call(
					jen.Map(jen.String()).Any().Values(jen.Dict{
						jen.Lit("jsonrpc"): jen.Lit("2.0"),
						jen.Lit("method"):  jen.Id("method"),
						jen.Lit("params"):  jen.Id("params"),
					}),
				),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Id("err")),
				),
				jen.If(jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Qual("fmt", "Fprintf").Call(jen.Id("w"), jen.Lit("event: notification\ndata: %s\n\n"), jen.Id("message")), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Id("err")),
				),
				jen.Return(jen.Nil()),
			)
		stmt.Line()

		stmt.Func().Id("sdkEventsStreamParams").
			Params(jen.Id("res").Op("*").Id("EventsStreamResult")).
			Map(jen.String()).Any().
			Block(
				jen.Id("params").Op(":=").Map(jen.String()).Any().Values(jen.Dict{
					jen.Lit("content"): jen.Index().Map(jen.String()).Any().Values(),
				}),
				jen.If(jen.Id("res").Op("==").Nil()).Block(
					jen.Return(jen.Id("params")),
				),
				jen.If(jen.Id("res").Dot("IsError").Op("!=").Nil()).Block(
					jen.Id("params").Index(jen.Lit("isError")).Op("=").Op("*").Id("res").Dot("IsError"),
				),
				jen.If(jen.Len(jen.Id("res").Dot("Content")).Op("==").Lit(0)).Block(
					jen.Return(jen.Id("params")),
				),
				jen.Id("content").Op(":=").Make(jen.Index().Map(jen.String()).Any(), jen.Lit(0), jen.Len(jen.Id("res").Dot("Content"))),
				jen.For(jen.List(jen.Id("_"), jen.Id("item")).Op(":=").Range().Id("res").Dot("Content")).Block(
					jen.If(jen.Id("item").Op("==").Nil()).Block(
						jen.Id("content").Op("=").Append(jen.Id("content"), jen.Nil()),
						jen.Continue(),
					),
					jen.Id("entry").Op(":=").Map(jen.String()).Any().Values(jen.Dict{
						jen.Lit("type"): jen.Id("item").Dot("Type"),
					}),
					jen.If(jen.Id("item").Dot("Text").Op("!=").Nil()).Block(
						jen.Id("entry").Index(jen.Lit("text")).Op("=").Op("*").Id("item").Dot("Text"),
					),
					jen.If(jen.Id("item").Dot("Data").Op("!=").Nil()).Block(
						jen.Id("entry").Index(jen.Lit("data")).Op("=").Op("*").Id("item").Dot("Data"),
					),
					jen.If(jen.Id("item").Dot("MimeType").Op("!=").Nil()).Block(
						jen.Id("entry").Index(jen.Lit("mimeType")).Op("=").Op("*").Id("item").Dot("MimeType"),
					),
					jen.If(jen.Id("item").Dot("URI").Op("!=").Nil()).Block(
						jen.Id("entry").Index(jen.Lit("uri")).Op("=").Op("*").Id("item").Dot("URI"),
					),
					jen.Id("content").Op("=").Append(jen.Id("content"), jen.Id("entry")),
				),
				jen.Id("params").Index(jen.Lit("content")).Op("=").Id("content"),
				jen.Return(jen.Id("params")),
			)
		stmt.Line()
	})
}

func sdkServerRegistrationSection(data *AdapterData) codegen.Section {
	return codegen.MustJenniferSection("mcp-sdk-server-registration", func(stmt *jen.Statement) {
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
				jen.List(jen.Id("data"), jen.Id("err")).Op(":=").Qual("encoding/json", "Marshal").Call(jen.Id("raw")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
				jen.Var().Id("annotations").Id("mcpsdk").Dot("ToolAnnotations"),
				jen.If(jen.Id("err").Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("data"), jen.Op("&").Id("annotations")), jen.Id("err").Op("!=").Nil()).Block(
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
					jen.Return(jen.Qual("encoding/json", "RawMessage").Call(jen.Lit(`{"type":"object"}`))),
				),
				jen.Return(jen.Qual("encoding/json", "RawMessage").Call(jen.Index().Byte().Call(jen.Id("raw")))),
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
			for _, tool := range data.Tools {
				if tool.AnnotationsJSON != "" {
					name := "annotations" + codegen.Goify(tool.Name, true)
					g.List(jen.Id(name), jen.Id("err")).Op(":=").Id("sdkToolAnnotations").Call(
						jen.Qual("encoding/json", "RawMessage").Call(jen.Index().Byte().Call(jen.Lit(tool.AnnotationsJSON))),
					)
					g.If(jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("tool %q annotations: %w"), jen.Lit(tool.Name), jen.Id("err"))),
					)
				}
				dict := jen.Dict{
					jen.Id("Name"):        jen.Lit(tool.Name),
					jen.Id("Description"): jen.Lit(tool.Description),
					jen.Id("InputSchema"): jen.Id("sdkToolInputSchema").Call(jen.Lit(tool.InputSchema)),
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

func emitSDKRegisterResources(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Id("registerSDKResources").
		Params(
			jen.Id("server").Op("*").Id("mcpsdk").Dot("Server"),
			jen.Id("adapter").Op("*").Id("MCPAdapter"),
			jen.Id("requestContext").Func().Params(jen.Qual("context", "Context"), jen.Op("*").Qual("net/http", "Request")).Qual("context", "Context"),
		).
		Error().
		BlockFunc(func(g *jen.Group) {
			if len(data.Resources) == 0 {
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
	return codegen.MustJenniferSection("mcp-sdk-server-handlers", func(stmt *jen.Statement) {
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
						jen.If(jen.Id("req").Op("!=").Nil().Op("&&").Id("req").Dot("Params").Op("!=").Nil()).Block(
							jen.Id("payload").Dot("Name").Op("=").Id("req").Dot("Params").Dot("Name"),
							jen.Id("payload").Dot("Arguments").Op("=").Id("req").Dot("Params").Dot("Arguments"),
						),
						jen.Id("ctx").Op("=").Id("a").Dot("sdkRequestContext").Call(jen.Id("ctx"), jen.Id("req").Dot("GetSession").Call(), jen.Id("req").Dot("GetExtra").Call(), jen.Id("requestContext")),
						jen.Id("stream").Op(":=").Op("&").Id("sdkToolCallCollector").Values(),
						jen.If(jen.Id("err").Op(":=").Id("a").Dot("ToolsCall").Call(jen.Id("ctx"), jen.Id("payload"), jen.Id("stream")), jen.Id("err").Op("!=").Nil()).Block(
							jen.Return(jen.Nil(), jen.Id("err")),
						),
						jen.Return(jen.Id("sdkCallToolResult").Call(jen.Id("stream").Dot("result").Call())),
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
							jen.If(jen.Id("req").Op("!=").Nil().Op("&&").Id("req").Dot("Params").Op("!=").Nil()).Block(
								jen.Id("payload").Dot("Name").Op("=").Id("req").Dot("Params").Dot("Name"),
								jen.If(jen.Id("req").Dot("Params").Dot("Arguments").Op("!=").Nil()).Block(
									jen.List(jen.Id("args"), jen.Id("err")).Op(":=").Qual("encoding/json", "Marshal").Call(jen.Id("req").Dot("Params").Dot("Arguments")),
									jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
									jen.Id("payload").Dot("Arguments").Op("=").Id("args"),
								),
							),
							jen.Id("ctx").Op("=").Id("a").Dot("sdkRequestContext").Call(jen.Id("ctx"), jen.Id("req").Dot("GetSession").Call(), jen.Id("req").Dot("GetExtra").Call(), jen.Id("requestContext")),
							jen.List(jen.Id("result"), jen.Id("err")).Op(":=").Id("a").Dot("PromptsGet").Call(jen.Id("ctx"), jen.Id("payload")),
							jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
							jen.Return(jen.Id("sdkGetPromptResult").Call(jen.Id("result"))),
						)),
				)
			stmt.Line()
		}
		if len(data.Resources) > 0 {
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
							jen.If(jen.Id("req").Op("!=").Nil().Op("&&").Id("req").Dot("Params").Op("!=").Nil()).Block(
								jen.Id("payload").Dot("URI").Op("=").Id("req").Dot("Params").Dot("URI"),
							),
							jen.Id("ctx").Op("=").Id("a").Dot("sdkRequestContext").Call(jen.Id("ctx"), jen.Id("req").Dot("GetSession").Call(), jen.Id("req").Dot("GetExtra").Call(), jen.Id("requestContext")),
							jen.List(jen.Id("result"), jen.Id("err")).Op(":=").Id("a").Dot("ResourcesRead").Call(jen.Id("ctx"), jen.Id("payload")),
							jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Return(jen.Nil(), jen.Id("err"))),
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
				jen.Id("sessionID").Op(":=").Id("session").Dot("ID").Call(),
				jen.If(jen.Id("sessionID").Op("==").Lit("")).Block(
					jen.Id("a").Dot("markInitializedSession").Call(jen.Lit("")),
					jen.Return(jen.Id("ctx")),
				),
				jen.Id("a").Dot("markInitializedSession").Call(jen.Id("sessionID")),
				jen.Return(jen.Id("mcpruntime").Dot("WithSessionID").Call(jen.Id("ctx"), jen.Id("sessionID"))),
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
				jen.If(jen.Id("extra").Op("!=").Nil().Op("&&").Id("extra").Dot("Header").Op("!=").Nil()).Block(
					jen.Id("req").Dot("Header").Op("=").Id("extra").Dot("Header").Dot("Clone").Call(),
				),
				jen.For(jen.List(jen.Id("key"), jen.Id("values")).Op(":=").Range().Id("mcpruntime").Dot("RequestHeadersFromContext").Call(jen.Id("ctx"))).Block(
					jen.Id("req").Dot("Header").Dot("Del").Call(jen.Id("key")),
					jen.For(jen.List(jen.Id("_"), jen.Id("value")).Op(":=").Range().Id("values")).Block(
						jen.Id("req").Dot("Header").Dot("Add").Call(jen.Id("key"), jen.Id("value")),
					),
				),
				jen.Return(jen.Id("req")),
			)
		stmt.Line()
	})
}

func sdkServerConversionSection(data *AdapterData) codegen.Section {
	return codegen.MustJenniferSection("mcp-sdk-server-conversions", func(stmt *jen.Statement) {
		emitSDKCollectorMethods(stmt)
		emitSDKCallToolResult(stmt)
		if len(data.StaticPrompts) > 0 || len(data.DynamicPrompts) > 0 {
			emitSDKPromptConversion(stmt)
		}
		if len(data.Resources) > 0 {
			emitSDKReadResourceConversion(stmt)
		}
		emitSDKContentConversions(stmt)
		emitSDKHelpers(stmt)
	})
}

func emitSDKCollectorMethods(stmt *jen.Statement) {
	stmt.Func().Params(jen.Id("c").Op("*").Id("sdkToolCallCollector")).
		Id("Send").
		Params(jen.Id("_").Qual("context", "Context"), jen.Id("event").Id("ToolsCallEvent")).
		Error().
		Block(
			jen.Id("res").Op(":=").Id("event").Assert(jen.Op("*").Id("ToolsCallResult")),
			jen.Id("c").Dot("parts").Op("=").Append(jen.Id("c").Dot("parts"), jen.Id("res")),
			jen.Return(jen.Nil()),
		)
	stmt.Line()
	stmt.Func().Params(jen.Id("c").Op("*").Id("sdkToolCallCollector")).
		Id("SendAndClose").
		Params(jen.Id("_").Qual("context", "Context"), jen.Id("event").Id("ToolsCallEvent")).
		Error().
		Block(
			jen.Id("res").Op(":=").Id("event").Assert(jen.Op("*").Id("ToolsCallResult")),
			jen.Id("c").Dot("final").Op("=").Id("res"),
			jen.Return(jen.Nil()),
		)
	stmt.Line()
	stmt.Func().Params(jen.Id("c").Op("*").Id("sdkToolCallCollector")).
		Id("SendError").
		Params(jen.Id("_").Qual("context", "Context"), jen.Id("_").String(), jen.Id("err").Error()).
		Error().
		Block(
			jen.Id("c").Dot("streamErr").Op("=").Id("err"),
			jen.Return(jen.Nil()),
		)
	stmt.Line()
	stmt.Func().Params(jen.Id("c").Op("*").Id("sdkToolCallCollector")).
		Id("result").Params().Op("*").Id("ToolsCallResult").
		Block(
			jen.If(jen.Id("c").Op("==").Nil()).Block(
				jen.Return(jen.Op("&").Id("ToolsCallResult").Values()),
			),
			jen.If(jen.Id("c").Dot("streamErr").Op("!=").Nil()).Block(
				jen.Id("item").Op(":=").Op("&").Id("ContentItem").Values(jen.Dict{
					jen.Id("Type"): jen.Lit("text"),
					jen.Id("Text"): jen.Id("stringPtr").Call(jen.Id("c").Dot("streamErr").Dot("Error").Call()),
				}),
				jen.Return(jen.Op("&").Id("ToolsCallResult").Values(jen.Dict{
					jen.Id("Content"): jen.Index().Op("*").Id("ContentItem").Values(jen.Id("item")),
					jen.Id("IsError"): jen.Id("boolPtr").Call(jen.True()),
				})),
			),
			jen.If(jen.Len(jen.Id("c").Dot("parts")).Op("==").Lit(0)).Block(
				jen.If(jen.Id("c").Dot("final").Op("==").Nil()).Block(
					jen.Return(jen.Op("&").Id("ToolsCallResult").Values()),
				),
				jen.Return(jen.Id("c").Dot("final")),
			),
			jen.Id("merged").Op(":=").Op("&").Id("ToolsCallResult").Values(),
			jen.For(jen.List(jen.Id("_"), jen.Id("part")).Op(":=").Range().Id("c").Dot("parts")).Block(
				jen.If(jen.Id("part").Op("==").Nil()).Block(jen.Continue()),
				jen.Id("merged").Dot("Content").Op("=").Append(jen.Id("merged").Dot("Content"), jen.Id("part").Dot("Content").Op("...")),
				jen.If(jen.Id("part").Dot("IsError").Op("!=").Nil().Op("&&").Op("*").Id("part").Dot("IsError")).Block(
					jen.Id("merged").Dot("IsError").Op("=").Id("boolPtr").Call(jen.True()),
				),
			),
			jen.If(jen.Id("c").Dot("final").Op("!=").Nil()).Block(
				jen.Id("merged").Dot("Content").Op("=").Append(jen.Id("merged").Dot("Content"), jen.Id("c").Dot("final").Dot("Content").Op("...")),
				jen.If(jen.Id("c").Dot("final").Dot("IsError").Op("!=").Nil()).Block(
					jen.Id("merged").Dot("IsError").Op("=").Id("c").Dot("final").Dot("IsError"),
				),
			),
			jen.Return(jen.Id("merged")),
		)
	stmt.Line()
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
			jen.If(jen.Id("result").Dot("StructuredContent").Op("!=").Nil()).Block(
				jen.Id("callResult").Dot("StructuredContent").Op("=").Id("result").Dot("StructuredContent"),
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
