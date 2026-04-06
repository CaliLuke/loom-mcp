package codegen

import (
	"path/filepath"

	"github.com/CaliLuke/loom-mcp/internal/upstreampaths"
	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/expr"
	"github.com/dave/jennifer/jen"
)

type (
	clientMethodInfo struct {
		Name     string
		IsMapped bool
	}

	clientAdapterFileData struct {
		*AdapterData
		ServicePkg       string
		MCPPkgAlias      string
		SvcJSONRPCCAlias string
		MCPJSONRPCCAlias string
		AllMethods       []clientMethodInfo
	}
)

func clientAdapterFile(genpkg string, svc *expr.ServiceExpr, data *AdapterData) *codegen.File {
	if data == nil {
		return nil
	}

	svcName := codegen.SnakeCase(svc.Name)
	fileData := buildClientAdapterFileData(svc, data)
	imports := []*codegen.ImportSpec{
		{Path: "bytes"},
		{Path: "context"},
		{Path: "encoding/json", Name: "stdjson"},
		{Path: "fmt"},
		{Path: "io"},
		{Path: "net/http"},
		{Path: "net/url"},
		{Path: "sync"},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/mcp", Name: "mcpruntime"},
		{Path: upstreampaths.LoomMCPHTTPImportPath, Name: "goahttp"},
		{Path: upstreampaths.LoomMCPJSONRPCImportPath, Name: "jsonrpc"},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/mcp/retry", Name: "retry"},
		{Path: genpkg + "/" + svcName, Name: svcName},
		{Path: genpkg + "/jsonrpc/" + svcName + "/client", Name: fileData.SvcJSONRPCCAlias},
		{Path: genpkg + "/mcp_" + svcName, Name: fileData.MCPPkgAlias},
		{Path: genpkg + "/jsonrpc/mcp_" + svcName + "/client", Name: fileData.MCPJSONRPCCAlias},
	}
	if data.NeedsQueryFormatting {
		imports = append(imports, &codegen.ImportSpec{Path: "strconv"})
	}

	return &codegen.File{
		Path: filepath.Join(codegen.Gendir, "mcp_"+svcName, "adapter", "client", "adapter.go"),
		Sections: []codegen.Section{
			codegen.Header("MCP client adapter exposing original service endpoints", fileData.MCPPkgAlias+"adapter", imports),
			codegen.MustJenniferSection("mcp-client-adapter", func(stmt *jen.Statement) {
				emitClientAdapterHelpers(stmt, fileData)
				emitClientAdapterNewEndpoints(stmt, fileData)
				emitClientAdapterNewClient(stmt, fileData)
			}),
		},
	}
}

func buildClientAdapterFileData(svc *expr.ServiceExpr, data *AdapterData) *clientAdapterFileData {
	svcName := codegen.SnakeCase(svc.Name)
	mcpPkgAlias := codegen.Goify("mcp_"+svcName, false)
	mapped := make(map[string]struct{}, len(data.Tools)+len(data.Resources)+len(data.DynamicPrompts)+len(data.Notifications))
	for _, tool := range data.Tools {
		mapped[tool.OriginalMethodName] = struct{}{}
	}
	for _, resource := range data.Resources {
		mapped[resource.OriginalMethodName] = struct{}{}
	}
	for _, prompt := range data.DynamicPrompts {
		mapped[prompt.OriginalMethodName] = struct{}{}
	}
	for _, notification := range data.Notifications {
		mapped[notification.OriginalMethodName] = struct{}{}
	}
	methods := make([]clientMethodInfo, len(svc.Methods))
	for i, method := range svc.Methods {
		methodName := codegen.Goify(method.Name, true)
		_, ok := mapped[methodName]
		methods[i] = clientMethodInfo{Name: methodName, IsMapped: ok}
	}
	return &clientAdapterFileData{
		AdapterData:      data,
		ServicePkg:       svcName,
		MCPPkgAlias:      mcpPkgAlias,
		SvcJSONRPCCAlias: svcName + "jsonrpcc",
		MCPJSONRPCCAlias: mcpPkgAlias + "jsonrpcc",
		AllMethods:       methods,
	}
}

func emitClientAdapterHelpers(stmt *jen.Statement, data *clientAdapterFileData) {
	stmt.Comment("encodeOriginalPayload serializes an original-service payload without a JSON-RPC envelope so MCP calls can forward raw arguments.").Line()
	stmt.Func().Id("encodeOriginalPayload").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("enc").Func().Params(jen.Op("*").Qual("net/http", "Request")).Id("goahttp").Dot("Encoder"),
			jen.Id("payload").Any(),
		).
		Params(jen.Index().Byte(), jen.Error()).
		Block(
			jen.List(jen.Id("req"), jen.Id("err")).Op(":=").Qual("net/http", "NewRequestWithContext").Call(jen.Id("ctx"), jen.Qual("net/http", "MethodPost"), jen.Lit(""), jen.Nil()),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.If(jen.Id("err").Op(":=").Id("enc").Call(jen.Id("req")).Dot("Encode").Call(jen.Id("payload")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.Return(jen.Qual("io", "ReadAll").Call(jen.Id("req").Dot("Body"))),
		)
	stmt.Line()

	if data.NeedsOriginalClient {
		stmt.Comment("decodeOriginalJSONRPCResult rehydrates one MCP result into the original service JSON-RPC response shape.").Line()
		stmt.Func().Id("decodeOriginalJSONRPCResult").
			Params(
				jen.Id("enc").Func().Params(jen.Op("*").Qual("net/http", "Request")).Id("goahttp").Dot("Encoder"),
				jen.Id("req").Op("*").Qual("net/http", "Request"),
				jen.Id("result").Index().Byte(),
				jen.Id("decode").Func().Params(jen.Op("*").Qual("net/http", "Response")).Params(jen.Any(), jen.Error()),
			).
			Params(jen.Any(), jen.Error()).
			Block(
				jen.Id("raw").Op(":=").Op("&").Id("jsonrpc").Dot("RawResponse").Values(jen.Dict{
					jen.Id("JSONRPC"): jen.Lit("2.0"),
					jen.Id("Result"):  jen.Id("result"),
				}),
				jen.If(jen.Id("err").Op(":=").Id("enc").Call(jen.Id("req")).Dot("Encode").Call(jen.Id("raw")), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				),
				jen.List(jen.Id("bodyBytes"), jen.Id("err")).Op(":=").Qual("io", "ReadAll").Call(jen.Id("req").Dot("Body")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				),
				jen.Id("resp").Op(":=").Op("&").Qual("net/http", "Response").Values(jen.Dict{
					jen.Id("StatusCode"): jen.Qual("net/http", "StatusOK"),
					jen.Id("Body"): jen.Qual("io", "NopCloser").Call(
						jen.Qual("bytes", "NewReader").Call(jen.Id("bodyBytes")),
					),
				}),
				jen.Return(jen.Id("decode").Call(jen.Id("resp"))),
			)
		stmt.Line()
	}

	stmt.Type().Id("sessionAwareDoer").Struct(
		jen.Id("base").Id("goahttp").Dot("Doer"),
		jen.Id("bootstrap").Func().Params(jen.Qual("context", "Context")).Error(),
		jen.Id("initMu").Qual("sync", "Mutex"),
		jen.Id("sessionMu").Qual("sync", "Mutex"),
		jen.Id("sessionID").String(),
		jen.Id("initialized").Bool(),
	)
	stmt.Line()

	stmt.Func().Params(jen.Id("d").Op("*").Id("sessionAwareDoer")).
		Id("Do").
		Params(jen.Id("req").Op("*").Qual("net/http", "Request")).
		Params(jen.Op("*").Qual("net/http", "Response"), jen.Error()).
		Block(
			jen.If(jen.Id("d").Op("==").Nil().Op("||").Id("d").Dot("base").Op("==").Nil()).Block(
				jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("mcp adapter doer is not configured"))),
			),
			jen.List(jen.Id("method"), jen.Id("err")).Op(":=").Id("jsonRPCMethod").Call(jen.Id("req")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.If(jen.Id("method").Op("!=").Lit("").Op("&&").Id("method").Op("!=").Lit("initialize")).Block(
				jen.If(jen.Id("err").Op(":=").Id("d").Dot("ensureInitialized").Call(jen.Id("req").Dot("Context").Call()), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				),
				jen.If(jen.Id("sessionID").Op(":=").Id("d").Dot("currentSessionID").Call(), jen.Id("sessionID").Op("!=").Lit("")).Block(
					jen.Id("req").Dot("Header").Dot("Set").Call(jen.Id("mcpruntime").Dot("HeaderKeySessionID"), jen.Id("sessionID")),
				),
			),
			jen.List(jen.Id("resp"), jen.Id("err")).Op(":=").Id("d").Dot("base").Dot("Do").Call(jen.Id("req")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.Id("d").Dot("captureSessionID").Call(jen.Id("resp")),
			jen.Return(jen.Id("resp"), jen.Nil()),
		)
	stmt.Line()

	stmt.Func().Params(jen.Id("d").Op("*").Id("sessionAwareDoer")).
		Id("ensureInitialized").
		Params(jen.Id("ctx").Qual("context", "Context")).
		Error().
		Block(
			jen.Id("d").Dot("initMu").Dot("Lock").Call(),
			jen.Defer().Id("d").Dot("initMu").Dot("Unlock").Call(),
			jen.If(jen.Id("d").Dot("initialized").Op("||").Id("d").Dot("currentSessionID").Call().Op("!=").Lit("")).Block(
				jen.Return(jen.Nil()),
			),
			jen.If(jen.Id("d").Dot("bootstrap").Op("==").Nil()).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("mcp adapter bootstrap is not configured"))),
			),
			jen.If(jen.Id("err").Op(":=").Id("d").Dot("bootstrap").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Id("err")),
			),
			jen.Id("d").Dot("initialized").Op("=").True(),
			jen.Return(jen.Nil()),
		)
	stmt.Line()

	stmt.Func().Params(jen.Id("d").Op("*").Id("sessionAwareDoer")).
		Id("currentSessionID").
		Params().
		String().
		Block(
			jen.Id("d").Dot("sessionMu").Dot("Lock").Call(),
			jen.Defer().Id("d").Dot("sessionMu").Dot("Unlock").Call(),
			jen.Return(jen.Id("d").Dot("sessionID")),
		)
	stmt.Line()

	stmt.Func().Params(jen.Id("d").Op("*").Id("sessionAwareDoer")).
		Id("captureSessionID").
		Params(jen.Id("resp").Op("*").Qual("net/http", "Response")).
		Block(
			jen.If(jen.Id("d").Op("==").Nil().Op("||").Id("resp").Op("==").Nil()).Block(
				jen.Return(),
			),
			jen.If(jen.Id("sessionID").Op(":=").Id("resp").Dot("Header").Dot("Get").Call(jen.Id("mcpruntime").Dot("HeaderKeySessionID")), jen.Id("sessionID").Op("!=").Lit("")).Block(
				jen.Id("d").Dot("sessionMu").Dot("Lock").Call(),
				jen.Id("d").Dot("sessionID").Op("=").Id("sessionID"),
				jen.Id("d").Dot("sessionMu").Dot("Unlock").Call(),
			),
		)
	stmt.Line()

	stmt.Func().Id("jsonRPCMethod").
		Params(jen.Id("req").Op("*").Qual("net/http", "Request")).
		Params(jen.String(), jen.Error()).
		Block(
			jen.If(jen.Id("req").Op("==").Nil().Op("||").Id("req").Dot("Body").Op("==").Nil()).Block(
				jen.Return(jen.Lit(""), jen.Nil()),
			),
			jen.List(jen.Id("body"), jen.Id("err")).Op(":=").Qual("io", "ReadAll").Call(jen.Id("req").Dot("Body")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Lit(""), jen.Id("err")),
			),
			jen.Id("req").Dot("Body").Op("=").Qual("io", "NopCloser").Call(jen.Qual("bytes", "NewReader").Call(jen.Id("body"))),
			jen.If(jen.Len(jen.Id("body")).Op("==").Lit(0)).Block(
				jen.Return(jen.Lit(""), jen.Nil()),
			),
			jen.Var().Id("envelope").Struct(
				jen.Id("Method").String().Tag(map[string]string{"json": "method"}),
			),
			jen.If(jen.Id("err").Op(":=").Id("stdjson").Dot("Unmarshal").Call(jen.Id("body"), jen.Op("&").Id("envelope")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Lit(""), jen.Nil()),
			),
			jen.Return(jen.Id("envelope").Dot("Method"), jen.Nil()),
		)
	stmt.Line()
}

func emitClientAdapterNewEndpoints(stmt *jen.Statement, data *clientAdapterFileData) {
	stmt.Comment("NewEndpoints creates endpoints that expose the original service API while routing mapped methods through MCP.").Line()
	stmt.Func().Id("NewEndpoints").
		Params(
			jen.Id("scheme").String(),
			jen.Id("host").String(),
			jen.Id("doer").Id("goahttp").Dot("Doer"),
			jen.Id("enc").Func().Params(jen.Op("*").Qual("net/http", "Request")).Id("goahttp").Dot("Encoder"),
			jen.Id("dec").Func().Params(jen.Op("*").Qual("net/http", "Response")).Id("goahttp").Dot("Decoder"),
			jen.Id("restore").Bool(),
		).
		Op("*").Id(data.ServicePkg).Dot("Endpoints").
		BlockFunc(func(g *jen.Group) {
			g.Id("sessionDoer").Op(":=").Op("&").Id("sessionAwareDoer").Values(jen.Dict{
				jen.Id("base"): jen.Id("doer"),
			})
			if data.NeedsMCPClient {
				g.Id("mcpC").Op(":=").Id(data.MCPJSONRPCCAlias).Dot("NewClient").Call(
					jen.Id("scheme"),
					jen.Id("host"),
					jen.Id("sessionDoer"),
					jen.Id("enc"),
					jen.Id("dec"),
					jen.Id("restore"),
				)
				g.Id("sessionDoer").Dot("bootstrap").Op("=").Func().
					Params(jen.Id("ctx").Qual("context", "Context")).
					Error().
					Block(
						jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id("mcpC").Dot("Initialize").Call().Call(
							jen.Id("ctx"),
							jen.Op("&").Id(data.MCPPkgAlias).Dot("InitializePayload").Values(jen.Dict{
								jen.Id("ProtocolVersion"): jen.Lit("2025-06-18"),
								jen.Id("ClientInfo"): jen.Op("&").Id(data.MCPPkgAlias).Dot("ClientInfo").Values(jen.Dict{
									jen.Id("Name"):    jen.Lit("loom-mcp-adapter"),
									jen.Id("Version"): jen.Lit("dev"),
								}),
							}),
						),
						jen.Return(jen.Id("err")),
					)
				if len(data.Tools) > 0 {
					g.Id("mcpCaller").Op(":=").Id(data.MCPJSONRPCCAlias).Dot("NewCaller").Call(jen.Id("mcpC"), jen.Lit(""))
				}
			}
			if data.NeedsOriginalClient {
				g.Id("origC").Op(":=").Id(data.SvcJSONRPCCAlias).Dot("NewClient").Call(
					jen.Id("scheme"),
					jen.Id("host"),
					jen.Id("doer"),
					jen.Id("enc"),
					jen.Id("dec"),
					jen.Id("restore"),
				)
			}
			g.Id("e").Op(":=").Op("&").Id(data.ServicePkg).Dot("Endpoints").Values()
			for _, tool := range data.Tools {
				emitClientToolEndpoint(g, data, tool)
			}
			for _, resource := range data.Resources {
				emitClientResourceEndpoint(g, data, resource)
			}
			for _, prompt := range data.DynamicPrompts {
				emitClientDynamicPromptEndpoint(g, data, prompt)
			}
			for _, notification := range data.Notifications {
				emitClientNotificationEndpoint(g, data, notification)
			}
			g.Return(jen.Id("e"))
		})
	stmt.Line()
}

func emitClientToolEndpoint(g *jen.Group, data *clientAdapterFileData, tool *ToolAdapter) {
	g.Commentf("Tool: %s -> %s", tool.Name, tool.OriginalMethodName)
	g.Id("e").Dot(tool.OriginalMethodName).Op("=").Func().
		Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("v").Any()).
		Params(jen.Any(), jen.Error()).
		BlockFunc(func(fn *jen.Group) {
			emitPayloadInit(fn, data.ServicePkg, tool.OriginalMethodName, tool.HasPayload)
			fn.List(jen.Id("args"), jen.Id("err")).Op(":=").Id("encodeOriginalPayload").Call(jen.Id("ctx"), jen.Id("enc"), jen.Id("payload"))
			fn.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			)
			fn.List(jen.Id("toolResp"), jen.Id("err")).Op(":=").Id("mcpCaller").Dot("CallTool").Call(
				jen.Id("ctx"),
				jen.Id("mcpruntime").Dot("CallRequest").Values(jen.Dict{
					jen.Id("Tool"):    jen.Lit(tool.Name),
					jen.Id("Payload"): jen.Id("args"),
				}),
			)
			fn.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Id("prompt").Op(":=").Id("retry").Dot("BuildRepairPrompt").Call(
					jen.Lit("tools/call:"+tool.Name),
					jen.Id("err").Dot("Error").Call(),
					jen.Lit(tool.ExampleArguments),
					jen.Lit(tool.InputSchema),
				),
				jen.Return(jen.Nil(), jen.Op("&").Id("retry").Dot("RetryableError").Values(jen.Dict{
					jen.Id("Prompt"): jen.Id("prompt"),
					jen.Id("Cause"):  jen.Id("err"),
				})),
			)
			if tool.HasResult {
				fn.If(jen.Len(jen.Id("toolResp").Dot("Result")).Op("==").Lit(0)).Block(
					jen.Id("prompt").Op(":=").Id("retry").Dot("BuildRepairPrompt").Call(
						jen.Lit("tools/call:"+tool.Name),
						jen.Lit("empty MCP tool response"),
						jen.Lit(tool.ExampleArguments),
						jen.Lit(tool.InputSchema),
					),
					jen.Return(jen.Nil(), jen.Op("&").Id("retry").Dot("RetryableError").Values(jen.Dict{
						jen.Id("Prompt"): jen.Id("prompt"),
						jen.Id("Cause"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("empty MCP tool response for " + tool.Name)),
					})),
				)
				fn.List(jen.Id("req3"), jen.Id("err")).Op(":=").Id("origC").Dot("Build"+tool.OriginalMethodName+"Request").Call(jen.Id("ctx"), jen.Id("v"))
				fn.If(jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				)
				fn.Id("decode").Op(":=").Id(data.SvcJSONRPCCAlias).Dot("Decode"+tool.OriginalMethodName+"Response").Call(jen.Id("dec"), jen.False())
				fn.Return(jen.Id("decodeOriginalJSONRPCResult").Call(jen.Id("enc"), jen.Id("req3"), jen.Id("toolResp").Dot("Result"), jen.Id("decode")))
				return
			}
			fn.Return(jen.Nil(), jen.Nil())
		})
	g.Line()
}

func emitClientResourceEndpoint(g *jen.Group, data *clientAdapterFileData, resource *ResourceAdapter) {
	g.Commentf("Resource: %s -> %s", resource.URI, resource.OriginalMethodName)
	g.Id("e").Dot(resource.OriginalMethodName).Op("=").Func().
		Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("v").Any()).
		Params(jen.Any(), jen.Error()).
		BlockFunc(func(fn *jen.Group) {
			fn.Id("uri").Op(":=").Lit(resource.URI)
			if resource.HasPayload {
				fn.Id("payload").Op(":=").Id("v").Assert(jen.Op("*").Id(data.ServicePkg).Dot(resource.OriginalMethodName + "Payload"))
				fn.Id("query").Op(":=").Qual("net/url", "Values").Values()
				for _, field := range resource.QueryFields {
					emitResourceQueryField(fn, field)
				}
				fn.If(jen.Id("encoded").Op(":=").Id("query").Dot("Encode").Call(), jen.Id("encoded").Op("!=").Lit("")).Block(
					jen.Id("uri").Op("=").Id("uri").Op("+").Lit("?").Op("+").Id("encoded"),
				)
			}
			fn.List(jen.Id("ires"), jen.Id("err")).Op(":=").Id("mcpC").Dot("ResourcesRead").Call().Call(
				jen.Id("ctx"),
				jen.Op("&").Id(data.MCPPkgAlias).Dot("ResourcesReadPayload").Values(jen.Dict{
					jen.Id("URI"): jen.Id("uri"),
				}),
			)
			fn.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			)
			fn.Id("rr").Op(":=").Id("ires").Assert(jen.Op("*").Id(data.MCPPkgAlias).Dot("ResourcesReadResult"))
			fn.If(
				jen.Id("rr").Op("==").Nil().Op("||").
					Id("rr").Dot("Contents").Op("==").Nil().Op("||").
					Len(jen.Id("rr").Dot("Contents")).Op("==").Lit(0).Op("||").
					Id("rr").Dot("Contents").Index(jen.Lit(0)).Op("==").Nil().Op("||").
					Id("rr").Dot("Contents").Index(jen.Lit(0)).Dot("Text").Op("==").Nil(),
			).Block(
				jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("empty MCP resource response for "+resource.URI))),
			)
			if resource.HasResult {
				fn.List(jen.Id("req3"), jen.Id("err")).Op(":=").Id("origC").Dot("Build"+resource.OriginalMethodName+"Request").Call(jen.Id("ctx"), jen.Id("v"))
				fn.If(jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				)
				fn.Id("decode").Op(":=").Id(data.SvcJSONRPCCAlias).Dot("Decode"+resource.OriginalMethodName+"Response").Call(jen.Id("dec"), jen.False())
				fn.Return(
					jen.Id("decodeOriginalJSONRPCResult").Call(
						jen.Id("enc"),
						jen.Id("req3"),
						jen.Index().Byte().Call(jen.Op("*").Id("rr").Dot("Contents").Index(jen.Lit(0)).Dot("Text")),
						jen.Id("decode"),
					),
				)
				return
			}
			fn.Return(jen.Nil(), jen.Nil())
		})
	g.Line()
}

func emitClientDynamicPromptEndpoint(g *jen.Group, data *clientAdapterFileData, prompt *DynamicPromptAdapter) {
	g.Commentf("Dynamic Prompt: %s -> %s", prompt.Name, prompt.OriginalMethodName)
	g.Id("e").Dot(prompt.OriginalMethodName).Op("=").Func().
		Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("v").Any()).
		Params(jen.Any(), jen.Error()).
		BlockFunc(func(fn *jen.Group) {
			emitPayloadInit(fn, data.ServicePkg, prompt.OriginalMethodName, prompt.HasPayload)
			fn.List(jen.Id("args"), jen.Id("err")).Op(":=").Id("encodeOriginalPayload").Call(jen.Id("ctx"), jen.Id("enc"), jen.Id("payload"))
			fn.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			)
			fn.List(jen.Id("ires"), jen.Id("err")).Op(":=").Id("mcpC").Dot("PromptsGet").Call().Call(
				jen.Id("ctx"),
				jen.Op("&").Id(data.MCPPkgAlias).Dot("PromptsGetPayload").Values(jen.Dict{
					jen.Id("Name"):      jen.Lit(prompt.Name),
					jen.Id("Arguments"): jen.Id("args"),
				}),
			)
			fn.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Id("repairPrompt").Op(":=").Id("retry").Dot("BuildRepairPrompt").Call(
					jen.Lit("prompts/get:"+prompt.Name),
					jen.Id("err").Dot("Error").Call(),
					jen.Lit(prompt.ExampleArguments),
					jen.Lit(""),
				),
				jen.Return(jen.Nil(), jen.Op("&").Id("retry").Dot("RetryableError").Values(jen.Dict{
					jen.Id("Prompt"): jen.Id("repairPrompt"),
					jen.Id("Cause"):  jen.Id("err"),
				})),
			)
			fn.Id("r").Op(":=").Id("ires").Assert(jen.Op("*").Id(data.MCPPkgAlias).Dot("PromptsGetResult"))
			fn.If(
				jen.Id("r").Op("==").Nil().Op("||").
					Id("r").Dot("Messages").Op("==").Nil().Op("||").
					Len(jen.Id("r").Dot("Messages")).Op("==").Lit(0).Op("||").
					Id("r").Dot("Messages").Index(jen.Lit(0)).Op("==").Nil().Op("||").
					Id("r").Dot("Messages").Index(jen.Lit(0)).Dot("Content").Op("==").Nil().Op("||").
					Id("r").Dot("Messages").Index(jen.Lit(0)).Dot("Content").Dot("Text").Op("==").Nil(),
			).Block(
				jen.Id("repairPrompt").Op(":=").Id("retry").Dot("BuildRepairPrompt").Call(
					jen.Lit("prompts/get:"+prompt.Name),
					jen.Lit("empty MCP prompt response"),
					jen.Lit(prompt.ExampleArguments),
					jen.Lit(""),
				),
				jen.Return(jen.Nil(), jen.Op("&").Id("retry").Dot("RetryableError").Values(jen.Dict{
					jen.Id("Prompt"): jen.Id("repairPrompt"),
					jen.Id("Cause"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("empty MCP prompt response for " + prompt.Name)),
				})),
			)
			fn.List(jen.Id("req3"), jen.Id("err")).Op(":=").Id("origC").Dot("Build"+prompt.OriginalMethodName+"Request").Call(jen.Id("ctx"), jen.Id("v"))
			fn.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			)
			fn.Id("decode").Op(":=").Id(data.SvcJSONRPCCAlias).Dot("Decode"+prompt.OriginalMethodName+"Response").Call(jen.Id("dec"), jen.False())
			fn.Return(
				jen.Id("decodeOriginalJSONRPCResult").Call(
					jen.Id("enc"),
					jen.Id("req3"),
					jen.Index().Byte().Call(jen.Op("*").Id("r").Dot("Messages").Index(jen.Lit(0)).Dot("Content").Dot("Text")),
					jen.Id("decode"),
				),
			)
		})
	g.Line()
}

func emitClientNotificationEndpoint(g *jen.Group, data *clientAdapterFileData, notification *NotificationAdapter) {
	g.Commentf("Notification: %s -> %s", notification.Name, notification.OriginalMethodName)
	g.Id("e").Dot(notification.OriginalMethodName).Op("=").Func().
		Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("v").Any()).
		Params(jen.Any(), jen.Error()).
		BlockFunc(func(fn *jen.Group) {
			fn.Id("payload").Op(":=").Id("v").Assert(jen.Op("*").Id(data.ServicePkg).Dot(notification.OriginalMethodName + "Payload"))
			fn.Id("notificationPayload").Op(":=").Op("&").Id(data.MCPPkgAlias).Dot("SendNotificationPayload").Values(jen.Dict{
				jen.Id("Type"): jen.Id("payload").Dot("Type"),
			})
			if notification.HasData {
				fn.Id("notificationPayload").Dot("Data").Op("=").Id("payload").Dot("Data")
			}
			if notification.HasMessage {
				if notification.MessagePointer {
					fn.Id("notificationPayload").Dot("Message").Op("=").Id("payload").Dot("Message")
				} else {
					fn.Id("message").Op(":=").Id("payload").Dot("Message")
					fn.Id("notificationPayload").Dot("Message").Op("=").Op("&").Id("message")
				}
			}
			fn.List(jen.Id("_"), jen.Id("err")).Op(":=").Id("mcpC").Dot("Notify"+codegen.Goify(notification.Name, true)).Call().Call(jen.Id("ctx"), jen.Id("notificationPayload"))
			fn.Return(jen.Nil(), jen.Id("err"))
		})
	g.Line()
}

func emitPayloadInit(g *jen.Group, servicePkg, method string, hasPayload bool) {
	g.Var().Id("payload").Any()
	if hasPayload {
		g.Id("payload").Op("=").Id("v").Assert(jen.Op("*").Id(servicePkg).Dot(method + "Payload"))
		return
	}
	g.Id("payload").Op("=").Struct().Values()
}

func emitResourceQueryField(g *jen.Group, field *ResourceQueryField) {
	body := func(target *jen.Group) {
		if field.Repeated {
			target.For(jen.List(jen.Id("_"), jen.Id("value")).Op(":=").Range().Add(rawExpr(field.CollectionExpr))).Block(
				jen.Id("query").Dot("Add").Call(jen.Lit(field.QueryKey), queryValueCode(field.FormatKind, jen.Id("value"))),
			)
			return
		}
		target.Id("query").Dot("Add").Call(jen.Lit(field.QueryKey), queryValueCode(field.FormatKind, rawExpr(field.ValueExpr)))
	}
	if field.GuardExpr != "" {
		g.If(rawExpr(field.GuardExpr)).BlockFunc(body)
		return
	}
	body(g)
}

func queryValueCode(formatKind string, value jen.Code) jen.Code {
	switch formatKind {
	case resourceQueryFormatString:
		return value
	case resourceQueryFormatBool:
		return jen.Qual("strconv", "FormatBool").Call(value)
	case resourceQueryFormatInt:
		return jen.Qual("strconv", "FormatInt").Call(jen.Int64().Call(value), jen.Lit(10))
	case resourceQueryFormatUint:
		return jen.Qual("strconv", "FormatUint").Call(jen.Uint64().Call(value), jen.Lit(10))
	case resourceQueryFormatFloat32:
		return jen.Qual("strconv", "FormatFloat").Call(jen.Float64().Call(value), jen.Op("'g'"), jen.Lit(-1), jen.Lit(32))
	case resourceQueryFormatFloat64:
		return jen.Qual("strconv", "FormatFloat").Call(value, jen.Op("'g'"), jen.Lit(-1), jen.Lit(64))
	default:
		panic("unsupported resource query format kind: " + formatKind)
	}
}

func rawExpr(expr string) jen.Code {
	return jen.Op(expr)
}

func emitClientAdapterNewClient(stmt *jen.Statement, data *clientAdapterFileData) {
	stmt.Commentf("NewClient returns *%s.Client using MCP-backed endpoints.", data.ServicePkg).Line()
	stmt.Func().Id("NewClient").
		Params(
			jen.Id("scheme").String(),
			jen.Id("host").String(),
			jen.Id("doer").Id("goahttp").Dot("Doer"),
			jen.Id("enc").Func().Params(jen.Op("*").Qual("net/http", "Request")).Id("goahttp").Dot("Encoder"),
			jen.Id("dec").Func().Params(jen.Op("*").Qual("net/http", "Response")).Id("goahttp").Dot("Decoder"),
			jen.Id("restore").Bool(),
		).
		Op("*").Id(data.ServicePkg).Dot("Client").
		BlockFunc(func(g *jen.Group) {
			g.Id("e").Op(":=").Id("NewEndpoints").Call(jen.Id("scheme"), jen.Id("host"), jen.Id("doer"), jen.Id("enc"), jen.Id("dec"), jen.Id("restore"))
			hasUnmapped := false
			for _, method := range data.AllMethods {
				if !method.IsMapped {
					hasUnmapped = true
					break
				}
			}
			if hasUnmapped {
				g.Id("origClient").Op(":=").Id(data.SvcJSONRPCCAlias).Dot("NewClient").Call(
					jen.Id("scheme"),
					jen.Id("host"),
					jen.Id("doer"),
					jen.Id("enc"),
					jen.Id("dec"),
					jen.Id("restore"),
				)
			}
			g.Return(
				jen.Id(data.ServicePkg).Dot("NewClient").CallFunc(func(args *jen.Group) {
					for _, method := range data.AllMethods {
						if method.IsMapped {
							args.Id("e").Dot(method.Name)
							continue
						}
						args.Id("origClient").Dot(method.Name).Call()
					}
				}),
			)
		})
	stmt.Line()
}
