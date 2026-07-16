package codegen

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/CaliLuke/loom/codegen"
	httpcodegen "github.com/CaliLuke/loom/http/codegen"
	"github.com/dave/jennifer/jen"
)

const jsonrpcServerHandlerSectionName = "jsonrpc-server-handler"

// applyMCPJSONRPCBatchHandlerSection replaces Loom's JSON-RPC HTTP handler
// section with the MCP-owned batch-aware implementation. The replacement is
// intentionally keyed by the stable upstream section identifier and fails if
// that ownership contract is not exact.
func applyMCPJSONRPCBatchHandlerSection(files []*codegen.File, data *httpcodegen.ServiceData) error {
	if data == nil {
		return errors.New("MCP JSON-RPC batch handler extension requires service data")
	}

	matched := 0
	var target *codegen.File
	for _, file := range files {
		if file == nil || filepath.Base(filepath.Dir(filepath.ToSlash(file.Path))) != "server" || filepath.Base(file.Path) != "server.go" {
			continue
		}

		fileMatches := 0
		for _, section := range file.AllSections() {
			if section.SectionName() == jsonrpcServerHandlerSectionName {
				fileMatches++
			}
		}
		if fileMatches != 1 {
			return fmt.Errorf(
				"upstream JSON-RPC batch handler contract changed in %s: expected one %q section, found %d",
				filepath.ToSlash(file.Path),
				jsonrpcServerHandlerSectionName,
				fileMatches,
			)
		}
		matched += fileMatches
		target = file
	}
	if matched != 1 {
		return fmt.Errorf(
			"upstream JSON-RPC batch handler contract changed: expected one %q section, found %d",
			jsonrpcServerHandlerSectionName,
			matched,
		)
	}

	sections := target.AllSections()
	updated := make([]codegen.Section, 0, len(sections))
	for _, section := range sections {
		if section.SectionName() == jsonrpcServerHandlerSectionName {
			updated = append(updated, mcpJSONRPCBatchHandlerSection(data))
			continue
		}
		updated = append(updated, section)
	}
	target.SetSections(updated)
	return nil
}

func mcpJSONRPCBatchHandlerSection(data *httpcodegen.ServiceData) codegen.Section {
	return codegen.NewJenniferSection(jsonrpcServerHandlerSectionName, func(stmt *jen.Statement) {
		emitMCPJSONRPCServeHTTP(stmt, data)
		emitMCPJSONRPCHandleHTTP(stmt, data)
		emitMCPJSONRPCHandleSingle(stmt, data)
		emitMCPJSONRPCHandleBatch(stmt, data)
		emitMCPJSONRPCProcessRequest(stmt, data)
		emitMCPJSONRPCBatchWriter(stmt)
		emitMCPJSONRPCBufferedBatchItem(stmt, data)
	})
}

func emitMCPJSONRPCServeHTTP(stmt *jen.Statement, data *httpcodegen.ServiceData) {
	if len(data.Endpoints) == 0 || mcpJSONRPCServiceHasMixedTransports(data) || httpcodegen.IsWebSocketEndpoint(data.Endpoints[0]) {
		return
	}
	stmt.Comment("serveHTTP handles JSON-RPC requests before server middleware.").Line()
	stmt.Func().Params(jen.Id("s").Op("*").Id(data.ServerStruct)).Id("serveHTTP").
		Params(
			jen.Id("w").Qual("net/http", "ResponseWriter"),
			jen.Id("r").Op("*").Qual("net/http", "Request"),
		).
		Block(jen.Id("s").Dot("handleHTTP").Call(jen.Id("w"), jen.Id("r")))
	stmt.Line()
}

func emitMCPJSONRPCHandleHTTP(stmt *jen.Statement, data *httpcodegen.ServiceData) {
	stmt.Comment("handleHTTP handles JSON-RPC requests.").Line()
	stmt.Func().Params(jen.Id("s").Op("*").Id(data.ServerStruct)).Id("handleHTTP").
		Params(
			jen.Id("w").Qual("net/http", "ResponseWriter"),
			jen.Id("r").Op("*").Qual("net/http", "Request"),
		).
		BlockFunc(func(g *jen.Group) {
			g.List(jen.Id("obs"), jen.Id("w")).Op(":=").Id("loomtransport").Dot("BeginJSONRPCRequest").Call(
				jen.Id("r").Dot("Context").Call(),
				jen.Id("w"),
				jen.Lit(data.Service.Name),
				jen.Id("r"),
			)
			g.Defer().Id("obs").Dot("End").Call()
			g.Id("r").Op("=").Id("r").Dot("WithContext").Call(
				jen.Id("loomtransport").Dot("WithRequestObserver").Call(jen.Id("r").Dot("Context").Call(), jen.Id("obs")),
			)
			g.Comment("Peek at the first byte to determine request type")
			g.Id("bufReader").Op(":=").Qual("bufio", "NewReader").Call(jen.Id("r").Dot("Body"))
			g.List(jen.Id("peek"), jen.Err()).Op(":=").Id("bufReader").Dot("Peek").Call(jen.Lit(1))
			g.If(jen.Err().Op("!=").Nil().Op("&&").Err().Op("!=").Qual("io", "EOF")).Block(
				jen.Id("r").Dot("Body").Dot("Close").Call(),
				jen.Id("obs").Dot("Fail").Call(jen.Id("loomtransport").Dot("ReasonRequestDecodeFailed")),
				jen.Id("s").Dot("errhandler").Call(
					jen.Id("r").Dot("Context").Call(),
					jen.Id("w"),
					jen.Qual("fmt", "Errorf").Call(jen.Lit("failed to read request body: %w"), jen.Err()),
				),
				jen.Return(),
			)
			g.Line()
			g.Comment("Wrap the buffered reader with the original closer")
			g.Id("r").Dot("Body").Op("=").Struct(
				jen.Qual("io", "Reader"),
				jen.Qual("io", "Closer"),
			).Values(jen.Dict{
				jen.Id("Reader"): jen.Id("bufReader"),
				jen.Id("Closer"): jen.Id("r").Dot("Body"),
			})
			g.Defer().Func().Params(jen.Id("r").Op("*").Qual("net/http", "Request")).Block(
				jen.If(
					jen.Err().Op(":=").Id("r").Dot("Body").Dot("Close").Call(),
					jen.Err().Op("!=").Nil(),
				).Block(
					jen.Id("s").Dot("errhandler").Call(
						jen.Id("r").Dot("Context").Call(),
						jen.Id("w"),
						jen.Qual("fmt", "Errorf").Call(jen.Lit("failed to close request body: %w"), jen.Err()),
					),
				),
			).Call(jen.Id("r"))
			g.Line()
			g.Comment("Route to appropriate handler")
			g.If(jen.Len(jen.Id("peek")).Op(">").Lit(0).Op("&&").Id("peek").Index(jen.Lit(0)).Op("==").LitByte('[')).Block(
				jen.Id("s").Dot("handleBatch").Call(jen.Id("w"), jen.Id("r")),
				jen.Return(),
			)
			g.Id("s").Dot("handleSingle").Call(jen.Id("w"), jen.Id("r"))
		})
	stmt.Line()
}

func emitMCPJSONRPCHandleSingle(stmt *jen.Statement, data *httpcodegen.ServiceData) {
	stmt.Comment("handleSingle handles a single JSON-RPC request.").Line()
	stmt.Func().Params(jen.Id("s").Op("*").Id(data.ServerStruct)).Id("handleSingle").
		Params(
			jen.Id("w").Qual("net/http", "ResponseWriter"),
			jen.Id("r").Op("*").Qual("net/http", "Request"),
		).
		Block(
			jen.Var().Id("req").Qual("github.com/CaliLuke/loom/jsonrpc", "RawRequest"),
			jen.If(
				jen.Err().Op(":=").Id("s").Dot("decoder").Call(jen.Id("r")).Dot("Decode").Call(jen.Op("&").Id("req")),
				jen.Err().Op("!=").Nil(),
			).Block(mcpJSONRPCParseErrorStatements("ReasonInvalidJSONRPCEnvelope")...),
			jen.Id("s").Dot("processRequest").Call(jen.Id("r").Dot("Context").Call(), jen.Id("r"), jen.Op("&").Id("req"), jen.Id("w")),
		)
	stmt.Line()
}

func emitMCPJSONRPCHandleBatch(stmt *jen.Statement, data *httpcodegen.ServiceData) {
	stmt.Comment("handleBatch handles a batch of JSON-RPC requests.").Line()
	stmt.Func().Params(jen.Id("s").Op("*").Id(data.ServerStruct)).Id("handleBatch").
		Params(
			jen.Id("w").Qual("net/http", "ResponseWriter"),
			jen.Id("r").Op("*").Qual("net/http", "Request"),
		).
		BlockFunc(func(g *jen.Group) {
			g.Var().Id("rawReqs").Index().Qual("encoding/json", "RawMessage")
			g.If(
				jen.Err().Op(":=").Id("s").Dot("decoder").Call(jen.Id("r")).Dot("Decode").Call(jen.Op("&").Id("rawReqs")),
				jen.Err().Op("!=").Nil(),
			).Block(mcpJSONRPCParseErrorStatements("ReasonInvalidJSONRPCBatch")...)
			g.If(jen.Len(jen.Id("rawReqs")).Op("==").Lit(0)).Block(
				jen.Id("loomtransport").Dot("RequestObserverFromContext").Call(jen.Id("r").Dot("Context").Call()).Dot("Fail").Call(jen.Id("loomtransport").Dot("ReasonInvalidJSONRPCBatch")),
				jen.Id("response").Op(":=").Qual("github.com/CaliLuke/loom/jsonrpc", "MakeErrorResponse").Call(
					jen.Nil(), jen.Qual("github.com/CaliLuke/loom/jsonrpc", "InvalidRequest"), jen.Lit("Invalid request"), jen.Nil(),
				),
				jen.If(
					jen.Id("encErr").Op(":=").Id("s").Dot("encoder").Call(jen.Id("r").Dot("Context").Call(), jen.Id("w")).Dot("Encode").Call(jen.Id("response")),
					jen.Id("encErr").Op("!=").Nil(),
				).Block(
					jen.Id("s").Dot("errhandler").Call(jen.Id("r").Dot("Context").Call(), jen.Id("w"), jen.Qual("fmt", "Errorf").Call(jen.Lit("failed to encode invalid batch response: %w"), jen.Id("encErr"))),
				),
				jen.Return(),
			)
			g.Id("loomtransport").Dot("RequestObserverFromContext").Call(jen.Id("r").Dot("Context").Call()).Dot("SetJSONRPC").Call(
				jen.Lit(""), jen.Lit(""), jen.Len(jen.Id("rawReqs")), jen.False(),
			)
			g.Id("w").Dot("Header").Call().Dot("Set").Call(jen.Lit("Content-Type"), jen.Lit("application/json"))
			g.Id("writer").Op(":=").Op("&").Id("batchWriter").Values(jen.Dict{jen.Id("Writer"): jen.Id("w")})
			g.For(jen.List(jen.Id("_"), jen.Id("rawReq")).Op(":=").Range().Id("rawReqs")).Block(
				jen.Var().Id("req").Qual("github.com/CaliLuke/loom/jsonrpc", "RawRequest"),
				jen.If(
					jen.Err().Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("rawReq"), jen.Op("&").Id("req")),
					jen.Err().Op("!=").Nil(),
				).Block(
					jen.Id("loomtransport").Dot("RequestObserverFromContext").Call(jen.Id("r").Dot("Context").Call()).Dot("Fail").Call(jen.Id("loomtransport").Dot("ReasonInvalidJSONRPCEnvelope")),
					jen.Id("s").Dot("encodeJSONRPCError").Call(
						jen.Id("r").Dot("Context").Call(),
						jen.Id("writer"),
						jen.Op("&").Qual("github.com/CaliLuke/loom/jsonrpc", "RawRequest").Values(),
						jen.Qual("github.com/CaliLuke/loom/jsonrpc", "InvalidRequest"),
						jen.Lit("Invalid request"),
						jen.Nil(),
					),
					jen.Continue(),
				),
				jen.Id("s").Dot("processBatchRequest").Call(jen.Id("r").Dot("Context").Call(), jen.Id("r"), jen.Op("&").Id("req"), jen.Id("writer")),
			)
			g.If(jen.Id("writer").Dot("written")).Block(
				jen.If(
					jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id("writer").Dot("Writer").Dot("Write").Call(jen.Index().Byte().Values(jen.LitByte(']'))),
					jen.Id("err").Op("!=").Nil(),
				).Block(
					jen.Id("loomtransport").Dot("RequestObserverFromContext").Call(jen.Id("r").Dot("Context").Call()).Dot("Fail").Call(jen.Id("loomtransport").Dot("ReasonResponseWriteFailed")),
					jen.Id("s").Dot("errhandler").Call(jen.Id("r").Dot("Context").Call(), jen.Id("w"), jen.Qual("fmt", "Errorf").Call(jen.Lit("failed to close JSON-RPC batch response: %w"), jen.Id("err"))),
					jen.Return(),
				),
			)
		})
	stmt.Line()
}

func mcpJSONRPCParseErrorStatements(reason string) []jen.Code {
	requestContext := func() *jen.Statement {
		return jen.Id("r").Dot("Context").Call()
	}
	return []jen.Code{
		jen.Id("loomtransport").Dot("RequestObserverFromContext").Call(requestContext()).Dot("Fail").Call(jen.Id("loomtransport").Dot(reason)),
		jen.Id("response").Op(":=").Qual("github.com/CaliLuke/loom/jsonrpc", "MakeErrorResponse").Call(
			jen.Nil(),
			jen.Qual("github.com/CaliLuke/loom/jsonrpc", "ParseError"),
			jen.Lit("Parse error"),
			jen.Nil(),
		),
		jen.If(
			jen.Id("encErr").Op(":=").Id("s").Dot("encoder").Call(requestContext(), jen.Id("w")).Dot("Encode").Call(jen.Id("response")),
			jen.Id("encErr").Op("!=").Nil(),
		).Block(
			jen.Id("s").Dot("errhandler").Call(
				requestContext(),
				jen.Id("w"),
				jen.Qual("fmt", "Errorf").Call(jen.Lit("failed to encode parse error response: %w"), jen.Id("encErr")),
			),
		),
		jen.Return(),
	}
}

func emitMCPJSONRPCProcessRequest(stmt *jen.Statement, data *httpcodegen.ServiceData) {
	stmt.Comment("processRequest processes a single JSON-RPC request.").Line()
	stmt.Func().Params(jen.Id("s").Op("*").Id(data.ServerStruct)).Id("processRequest").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("r").Op("*").Qual("net/http", "Request"),
			jen.Id("req").Op("*").Qual("github.com/CaliLuke/loom/jsonrpc", "RawRequest"),
			jen.Id("w").Qual("net/http", "ResponseWriter"),
		).
		BlockFunc(func(g *jen.Group) {
			g.Id("loomtransport").Dot("RequestObserverFromContext").Call(jen.Id("ctx")).Dot("SetJSONRPC").Call(
				jen.Id("req").Dot("Method"),
				jen.Qual("github.com/CaliLuke/loom/jsonrpc", "IDToString").Call(jen.Id("req").Dot("ID")),
				jen.Lit(0),
				jen.Op("!").Id("req").Dot("HasID"),
			)
			emitMCPJSONRPCInvalidRequest(g, jen.Id("req").Dot("Invalid"), "Invalid request", "ReasonInvalidJSONRPCEnvelope")
			emitMCPJSONRPCInvalidRequest(g, jen.Id("req").Dot("JSONRPC").Op("!=").Lit("2.0"), "Invalid request", "ReasonInvalidJSONRPCEnvelope")
			emitMCPJSONRPCInvalidRequest(g, jen.Id("req").Dot("Method").Op("==").Lit(""), "Missing method field", "ReasonInvalidJSONRPCMethod")
			g.Switch(jen.Id("req").Dot("Method")).BlockFunc(func(sg *jen.Group) {
				for _, endpoint := range data.Endpoints {
					sg.Case(jen.Lit(endpoint.Method.Name)).Block(
						jen.If(
							jen.Err().Op(":=").Id("s").Dot(endpoint.Method.VarName).Call(jen.Id("ctx"), jen.Id("r"), jen.Id("req"), jen.Id("w")),
							jen.Err().Op("!=").Nil(),
						).Block(
							jen.Id("loomtransport").Dot("RequestObserverFromContext").Call(jen.Id("ctx")).Dot("Fail").Call(jen.Id("loomtransport").Dot("ReasonHandlerError")),
							jen.Id("s").Dot("errhandler").Call(jen.Id("ctx"), jen.Id("w"), jen.Qual("fmt", "Errorf").Call(jen.Lit("handler error for "+endpoint.Method.Name+": %w"), jen.Err())),
						),
					)
				}
				sg.Default().Block(
					jen.Id("loomtransport").Dot("RequestObserverFromContext").Call(jen.Id("ctx")).Dot("Fail").Call(jen.Id("loomtransport").Dot("ReasonUnsupportedMethod")),
					jen.Id("s").Dot("encodeJSONRPCError").Call(
						jen.Id("ctx"), jen.Id("w"), jen.Id("req"),
						jen.Qual("github.com/CaliLuke/loom/jsonrpc", "MethodNotFound"),
						jen.Lit("Method not found"),
						jen.Nil(),
					),
				)
			})
		})
	stmt.Line()
}

func emitMCPJSONRPCInvalidRequest(g *jen.Group, condition jen.Code, message string, reason string) {
	g.If(condition).Block(
		jen.Id("loomtransport").Dot("RequestObserverFromContext").Call(jen.Id("ctx")).Dot("Fail").Call(jen.Id("loomtransport").Dot(reason)),
		jen.Id("s").Dot("encodeJSONRPCError").Call(
			jen.Id("ctx"), jen.Id("w"), jen.Id("req"),
			jen.Qual("github.com/CaliLuke/loom/jsonrpc", "InvalidRequest"),
			jen.Lit(message),
			jen.Nil(),
		),
		jen.Return(),
	)
}

func emitMCPJSONRPCBatchWriter(stmt *jen.Statement) {
	stmt.Comment("batchWriter is a helper type that implements http.ResponseWriter for writing multiple JSON-RPC responses").Line()
	stmt.Type().Id("batchWriter").Struct(
		jen.Qual("io", "Writer"),
		jen.Id("header").Qual("net/http", "Header"),
		jen.Id("written").Bool(),
	)
	stmt.Line()
	stmt.Func().Params(jen.Id("rb").Op("*").Id("batchWriter")).Id("Header").Params().Qual("net/http", "Header").Block(
		jen.If(jen.Id("rb").Dot("header").Op("==").Nil()).Block(
			jen.Id("rb").Dot("header").Op("=").Make(jen.Qual("net/http", "Header")),
		),
		jen.Return(jen.Id("rb").Dot("header")),
	)
	stmt.Line()
	stmt.Func().Params(jen.Id("rb").Op("*").Id("batchWriter")).Id("WriteHeader").Params(jen.Id("_").Int()).Block(
		jen.Comment("JSON-RPC batch items do not control the outer HTTP status."),
	)
	stmt.Line()
	stmt.Func().Params(jen.Id("rb").Op("*").Id("batchWriter")).Id("Write").Params(jen.Id("data").Index().Byte()).Params(jen.Int(), jen.Error()).BlockFunc(func(g *jen.Group) {
		g.Id("delimiter").Op(":=").LitByte(',')
		g.If(jen.Op("!").Id("rb").Dot("written")).Block(jen.Id("delimiter").Op("=").LitByte('['))
		g.If(
			jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id("rb").Dot("Writer").Dot("Write").Call(jen.Index().Byte().Values(jen.Id("delimiter"))),
			jen.Id("err").Op("!=").Nil(),
		).Block(
			jen.Return(jen.Lit(0), jen.Qual("fmt", "Errorf").Call(jen.Lit("write JSON-RPC batch delimiter: %w"), jen.Id("err"))),
		)
		g.Id("rb").Dot("written").Op("=").True()
		g.Return(jen.Id("rb").Dot("Writer").Dot("Write").Call(jen.Id("data")))
	})
	stmt.Line()
}

func emitMCPJSONRPCBufferedBatchItem(stmt *jen.Statement, data *httpcodegen.ServiceData) {
	stmt.Comment("mcpBatchResponseWriter buffers one batch item so SSE frames cannot corrupt the outer JSON array.").Line()
	stmt.Type().Id("mcpBatchResponseWriter").Struct(
		jen.Id("header").Qual("net/http", "Header"),
		jen.Id("body").Qual("bytes", "Buffer"),
		jen.Id("statusCode").Int(),
	)
	stmt.Line()
	stmt.Func().Params(jen.Id("w").Op("*").Id("mcpBatchResponseWriter")).Id("Header").Params().Qual("net/http", "Header").Block(
		jen.If(jen.Id("w").Dot("header").Op("==").Nil()).Block(jen.Id("w").Dot("header").Op("=").Make(jen.Qual("net/http", "Header"))),
		jen.Return(jen.Id("w").Dot("header")),
	)
	stmt.Line()
	stmt.Func().Params(jen.Id("w").Op("*").Id("mcpBatchResponseWriter")).Id("WriteHeader").Params(jen.Id("statusCode").Int()).Block(
		jen.If(jen.Id("w").Dot("statusCode").Op("==").Lit(0)).Block(jen.Id("w").Dot("statusCode").Op("=").Id("statusCode")),
	)
	stmt.Line()
	stmt.Func().Params(jen.Id("w").Op("*").Id("mcpBatchResponseWriter")).Id("Write").Params(jen.Id("data").Index().Byte()).Params(jen.Int(), jen.Error()).Block(
		jen.If(jen.Id("w").Dot("statusCode").Op("==").Lit(0)).Block(jen.Id("w").Dot("statusCode").Op("=").Qual("net/http", "StatusOK")),
		jen.Return(jen.Id("w").Dot("body").Dot("Write").Call(jen.Id("data"))),
	)
	stmt.Line()
	stmt.Func().Params(jen.Id("w").Op("*").Id("mcpBatchResponseWriter")).Id("Flush").Params().Block()
	stmt.Line()
	stmt.Func().Params(jen.Id("s").Op("*").Id(data.ServerStruct)).Id("processBatchRequest").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("r").Op("*").Qual("net/http", "Request"),
		jen.Id("req").Op("*").Qual("github.com/CaliLuke/loom/jsonrpc", "RawRequest"),
		jen.Id("writer").Op("*").Id("batchWriter"),
	).BlockFunc(func(g *jen.Group) {
		g.Id("buffer").Op(":=").Op("&").Id("mcpBatchResponseWriter").Values()
		g.Id("s").Dot("processRequest").Call(jen.Id("ctx"), jen.Id("r"), jen.Id("req"), jen.Id("buffer"))
		g.Id("body").Op(":=").Qual("bytes", "TrimSpace").Call(jen.Id("buffer").Dot("body").Dot("Bytes").Call())
		g.If(jen.Len(jen.Id("body")).Op("==").Lit(0)).Block(jen.Return())
		g.If(jen.Op("!").Qual("strings", "Contains").Call(jen.Id("buffer").Dot("Header").Call().Dot("Get").Call(jen.Lit("Content-Type")), jen.Lit("text/event-stream"))).Block(
			jen.If(
				jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id("writer").Dot("Write").Call(jen.Id("body")),
				jen.Id("err").Op("!=").Nil(),
			).Block(jen.Id("s").Dot("errhandler").Call(jen.Id("ctx"), jen.Id("writer"), jen.Qual("fmt", "Errorf").Call(jen.Lit("failed to write buffered batch response: %w"), jen.Id("err")))),
			jen.Return(),
		)
		g.If(
			jen.Id("response").Op(":=").Id("finalMCPBatchSSEResponse").Call(jen.Id("body")),
			jen.Len(jen.Id("response")).Op(">").Lit(0),
		).Block(
			jen.If(
				jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id("writer").Dot("Write").Call(jen.Id("response")),
				jen.Id("err").Op("!=").Nil(),
			).Block(jen.Id("s").Dot("errhandler").Call(jen.Id("ctx"), jen.Id("writer"), jen.Qual("fmt", "Errorf").Call(jen.Lit("failed to write buffered batch stream response: %w"), jen.Id("err")))),
			jen.Return(),
		)
		g.If(jen.Id("req").Dot("HasID")).Block(
			jen.Id("response").Op(":=").Qual("github.com/CaliLuke/loom/jsonrpc", "MakeErrorResponse").Call(
				jen.Id("req").Dot("ID"),
				jen.Qual("github.com/CaliLuke/loom/jsonrpc", "InternalError"),
				jen.Lit("streaming method did not produce a final response"),
				jen.Nil(),
			),
			jen.List(jen.Id("data"), jen.Id("err")).Op(":=").Qual("encoding/json", "Marshal").Call(jen.Id("response")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Id("s").Dot("errhandler").Call(jen.Id("ctx"), jen.Id("writer"), jen.Qual("fmt", "Errorf").Call(jen.Lit("failed to encode buffered batch stream error: %w"), jen.Id("err"))),
				jen.Return(),
			),
			jen.If(
				jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id("writer").Dot("Write").Call(jen.Id("data")),
				jen.Id("err").Op("!=").Nil(),
			).Block(jen.Id("s").Dot("errhandler").Call(jen.Id("ctx"), jen.Id("writer"), jen.Qual("fmt", "Errorf").Call(jen.Lit("failed to write buffered batch stream error: %w"), jen.Id("err")))),
		)
	})
	stmt.Line()
	stmt.Func().Id("finalMCPBatchSSEResponse").Params(jen.Id("body").Index().Byte()).Index().Byte().BlockFunc(func(g *jen.Group) {
		g.Var().Id("response").Index().Byte()
		g.For(jen.List(jen.Id("_"), jen.Id("frame")).Op(":=").Range().Qual("strings", "Split").Call(jen.String().Call(jen.Id("body")), jen.Lit("\n\n"))).Block(
			jen.For(jen.List(jen.Id("_"), jen.Id("line")).Op(":=").Range().Qual("strings", "Split").Call(jen.Id("frame"), jen.Lit("\n"))).Block(
				jen.If(jen.Op("!").Qual("strings", "HasPrefix").Call(jen.Id("line"), jen.Lit("data:"))).Block(jen.Continue()),
				jen.Id("data").Op(":=").Qual("bytes", "TrimSpace").Call(jen.Index().Byte().Call(jen.Qual("strings", "TrimPrefix").Call(jen.Id("line"), jen.Lit("data:")))),
				jen.If(jen.Len(jen.Id("data")).Op("==").Lit(0)).Block(jen.Continue()),
				jen.Var().Id("envelope").Id("mcpJSONRPCEnvelope"),
				jen.If(jen.Err().Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("data"), jen.Op("&").Id("envelope")), jen.Err().Op("!=").Nil()).Block(jen.Continue()),
				jen.If(
					jen.Id("envelope").Dot("Method").Op("==").Lit("").Op("&&").Len(jen.Id("envelope").Dot("ID")).Op(">").Lit(0).
						Op("&&").Parens(jen.Len(jen.Id("envelope").Dot("Result")).Op(">").Lit(0).Op("||").Len(jen.Id("envelope").Dot("Error")).Op(">").Lit(0)),
				).Block(jen.Id("response").Op("=").Append(jen.Id("response").Index(jen.Empty(), jen.Lit(0)), jen.Id("data").Op("..."))),
			),
		)
		g.Return(jen.Id("response"))
	})
	stmt.Line()
}

func mcpJSONRPCServiceHasMixedTransports(data *httpcodegen.ServiceData) bool {
	hasHTTP := false
	hasSSE := false
	for _, endpoint := range data.Endpoints {
		if httpcodegen.IsSSEEndpoint(endpoint) {
			hasSSE = true
			continue
		}
		hasHTTP = true
	}
	return hasHTTP && hasSSE
}
