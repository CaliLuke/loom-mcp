package codegen

import (
	"path/filepath"

	"github.com/CaliLuke/loom/codegen"
	"github.com/dave/jennifer/jen"
)

func clientCallerFile(data *AdapterData, svcName string) *codegen.File {
	if data == nil || data.ClientCaller == nil {
		return nil
	}
	path := filepath.Join(codegen.Gendir, "jsonrpc", "mcp_"+svcName, "client", "caller.go")
	imports := []*codegen.ImportSpec{
		{Path: "context"},
		{Path: "encoding/json"},
		{Path: "errors"},
		{Path: "fmt"},
		{Path: "io"},
		{Path: data.ClientCaller.MCPImportPath, Name: "mcppkg"},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/mcp", Name: "mcpruntime"},
	}
	return &codegen.File{
		Path: path,
		Sections: []codegen.Section{
			codegen.Header("MCP JSON-RPC client caller", "client", imports),
			codegen.MustJenniferSection("mcp-client-caller", func(stmt *jen.Statement) {
				emitCallerType(stmt)
				emitCallerConstructor(stmt)
				emitCallerCallTool(stmt)
				emitCallerNormalizer(stmt)
			}),
		},
	}
}

func emitCallerType(stmt *jen.Statement) {
	stmt.Comment("Caller adapts the generated MCP JSON-RPC client to the runtime Caller interface.").Line()
	stmt.Type().Id("Caller").Struct(
		jen.Id("suite").String(),
		jen.Id("client").Op("*").Id("Client"),
	)
	stmt.Line()
}

func emitCallerConstructor(stmt *jen.Statement) {
	stmt.Comment("NewCaller wraps the generated Client so it can register with the loom-mcp runtime.").Line()
	stmt.Func().Id("NewCaller").
		Params(jen.Id("client").Op("*").Id("Client"), jen.Id("suite").String()).
		Id("mcpruntime").Dot("Caller").
		Block(
			jen.Return(jen.Id("Caller").Values(jen.Dict{
				jen.Id("suite"):  jen.Id("suite"),
				jen.Id("client"): jen.Id("client"),
			})),
		)
	stmt.Line()
}

func emitCallerCallTool(stmt *jen.Statement) {
	stmt.Comment("CallTool invokes tools/call via the generated JSON-RPC client and normalizes the response.").Line()
	stmt.Func().Params(jen.Id("c").Id("Caller")).
		Id("CallTool").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("req").Id("mcpruntime").Dot("CallRequest"),
		).
		Params(
			jen.Id("mcpruntime").Dot("CallResponse"),
			jen.Error(),
		).
		BlockFunc(func(g *jen.Group) {
			emitCallerRequireClient(g)
			emitCallerBuildPayload(g)
			emitCallerCallStream(g)
			emitCallerMergeEvents(g)
			emitCallerReturnMerged(g)
		})
	stmt.Line()
}

func emitCallerNormalizer(stmt *jen.Statement) {
	stmt.Func().Id("normalizeToolResult").
		Params(jen.Id("last").Op("*").Id("mcppkg").Dot("ToolsCallResult")).
		Params(
			jen.Id("mcpruntime").Dot("CallResponse"),
			jen.Error(),
		).
		BlockFunc(func(g *jen.Group) {
			g.Id("textParts").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Len(jen.Id("last").Dot("Content")))
			g.For(jen.List(jen.Id("_"), jen.Id("item")).Op(":=").Range().Id("last").Dot("Content")).Block(
				jen.If(jen.Id("item").Dot("Text").Op("!=").Nil()).Block(
					jen.Id("textParts").Op("=").Append(jen.Id("textParts"), jen.Op("*").Id("item").Dot("Text")),
				),
			)
			g.Var().Id("fallback").Any()
			g.If(jen.Len(jen.Id("last").Dot("StructuredContent")).Op(">").Lit(0)).Block(
				jen.Var().Id("decoded").Any(),
				jen.If(jen.Id("err").Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("last").Dot("StructuredContent"), jen.Op("&").Id("decoded")), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Id("mcpruntime").Dot("CallResponse").Values(), jen.Qual("fmt", "Errorf").Call(jen.Lit("failed to decode structured content: %w"), jen.Id("err"))),
				),
				jen.Id("fallback").Op("=").Id("decoded"),
			).Else().If(jen.Len(jen.Id("last").Dot("Content")).Op(">").Lit(0)).Block(
				jen.Id("fallback").Op("=").Id("last").Dot("Content").Index(jen.Lit(0)),
			)
			g.If(jen.Id("last").Dot("IsError").Op("!=").Nil().Op("&&").Op("*").Id("last").Dot("IsError")).Block(
				jen.Return(
					jen.Id("mcpruntime").Dot("CallResponse").Values(),
					jen.Id("mcpruntime").Dot("ToolCallErrorFromResponse").Call(
						jen.Id("textParts"),
						jen.Id("fallback"),
					),
				),
			)
			g.Return(
				jen.Id("mcpruntime").Dot("NormalizeToolCallResponse").Call(
					jen.Id("textParts"),
					jen.Id("last").Dot("Content"),
					jen.Id("fallback"),
				),
			)
		})
	stmt.Line()
}

func emitCallerRequireClient(g *jen.Group) {
	g.If(jen.Id("c").Dot("client").Op("==").Nil()).Block(
		jen.Return(
			jen.Id("mcpruntime").Dot("CallResponse").Values(),
			jen.Qual("errors", "New").Call(jen.Lit("mcp client not configured")),
		),
	)
}

func emitCallerBuildPayload(g *jen.Group) {
	g.Id("payload").Op(":=").Op("&").Id("mcppkg").Dot("ToolsCallPayload").Values(jen.Dict{
		jen.Id("Name"):      jen.Id("req").Dot("Tool"),
		jen.Id("Arguments"): jen.Qual("encoding/json", "RawMessage").Call(jen.Id("req").Dot("Payload")),
	})
}

func emitCallerCallStream(g *jen.Group) {
	g.Id("streamEndpoint").Op(":=").Id("c").Dot("client").Dot("ToolsCall").Call()
	g.List(jen.Id("stream"), jen.Id("err")).Op(":=").Id("streamEndpoint").Call(jen.Id("ctx"), jen.Id("payload"))
	g.If(jen.Id("err").Op("!=").Nil()).Block(
		jen.Return(jen.Id("mcpruntime").Dot("CallResponse").Values(), jen.Id("err")),
	)
	g.List(jen.Id("clientStream"), jen.Id("ok")).Op(":=").Id("stream").Assert(jen.Op("*").Id("ToolsCallClientStream"))
	g.If(jen.Op("!").Id("ok")).Block(
		jen.Return(
			jen.Id("mcpruntime").Dot("CallResponse").Values(),
			jen.Qual("errors", "New").Call(jen.Lit("invalid tools/call stream type")),
		),
	)
}

func emitCallerMergeEvents(g *jen.Group) {
	g.Var().Id("merged").Op("*").Id("mcppkg").Dot("ToolsCallResult")
	g.Id("eventCount").Op(":=").Lit(0)
	g.For().Block(
		jen.List(jen.Id("ev"), jen.Id("recvErr")).Op(":=").Id("clientStream").Dot("Recv").Call(jen.Id("ctx")),
		jen.If(jen.Id("recvErr").Op("==").Qual("io", "EOF")).Block(jen.Break()),
		jen.If(jen.Id("recvErr").Op("!=").Nil()).Block(
			jen.Return(jen.Id("mcpruntime").Dot("CallResponse").Values(), jen.Id("recvErr")),
		),
		jen.If(jen.Id("ev").Op("==").Nil()).Block(jen.Continue()),
		jen.Id("eventCount").Op("++"),
		jen.If(jen.Id("merged").Op("==").Nil()).Block(
			jen.Id("merged").Op("=").Op("&").Id("mcppkg").Dot("ToolsCallResult").Values(),
		),
		jen.Id("merged").Dot("Content").Op("=").Append(jen.Id("merged").Dot("Content"), jen.Id("ev").Dot("Content").Op("...")),
		jen.If(jen.Id("ev").Dot("StructuredContent").Op("!=").Nil()).Block(
			jen.Id("merged").Dot("StructuredContent").Op("=").Id("ev").Dot("StructuredContent"),
		),
		jen.If(jen.Id("ev").Dot("IsError").Op("!=").Nil()).Block(
			jen.Id("merged").Dot("IsError").Op("=").Id("ev").Dot("IsError"),
		),
	)
}

func emitCallerReturnMerged(g *jen.Group) {
	g.If(jen.Id("merged").Op("==").Nil().Op("||").Len(jen.Id("merged").Dot("Content")).Op("==").Lit(0)).Block(
		jen.Return(
			jen.Id("mcpruntime").Dot("CallResponse").Values(),
			jen.Qual("fmt", "Errorf").Call(
				jen.Lit("empty MCP response for suite %q tool %q: stream ended after %d events with no content"),
				jen.Id("c").Dot("suite"),
				jen.Id("req").Dot("Tool"),
				jen.Id("eventCount"),
			),
		),
	).Else().Block(
		jen.Return(jen.Id("normalizeToolResult").Call(jen.Id("merged"))),
	)
}
