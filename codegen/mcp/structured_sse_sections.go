package codegen

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/CaliLuke/loom/codegen"
	httpcodegen "github.com/CaliLuke/loom/http/codegen"
	"github.com/dave/jennifer/jen"
)

const jsonrpcSSEServerStreamSectionName = "jsonrpc-sse-server-stream"

// ownMCPJSONRPCSSEStreamSections replaces every upstream endpoint SSE stream
// section with an MCP-owned Jennifer section. Endpoint order is part of the
// upstream ServiceData contract and exact cardinality is enforced so generator
// drift cannot silently attach one endpoint's transport behavior to another.
func ownMCPJSONRPCSSEStreamSections(files []*codegen.File, data *httpcodegen.ServiceData) error {
	if data == nil {
		return errors.New("MCP JSON-RPC SSE extension requires service data")
	}
	endpoints := mcpSSEEndpoints(data)
	if len(endpoints) == 0 {
		return nil
	}

	matched := 0
	matchedFiles := 0
	for _, file := range files {
		if !isJSONRPCServerFile(file, "stream.go") {
			continue
		}
		matchedFiles++
		sections := file.AllSections()
		updated := make([]codegen.Section, 0, len(sections))
		for _, section := range sections {
			if section.SectionName() != jsonrpcSSEServerStreamSectionName {
				updated = append(updated, section)
				continue
			}
			if matched >= len(endpoints) {
				return fmt.Errorf(
					"upstream JSON-RPC SSE extension contract changed in %s: expected %d %q sections, found more",
					filepath.ToSlash(file.Path),
					len(endpoints),
					jsonrpcSSEServerStreamSectionName,
				)
			}
			updated = append(updated, mcpJSONRPCSSEServerStreamSection(endpoints[matched]))
			matched++
		}
		file.SetSections(updated)
		if header := file.HeaderTemplate(); header != nil {
			codegen.AddImport(header, &codegen.ImportSpec{Path: "github.com/CaliLuke/loom-mcp/v2/runtime/mcp", Name: "mcpruntime"})
		}
	}
	if matchedFiles != 1 || matched != len(endpoints) {
		return fmt.Errorf(
			"upstream JSON-RPC SSE extension contract changed: expected one stream.go and %d %q sections, found files=%d sections=%d",
			len(endpoints),
			jsonrpcSSEServerStreamSectionName,
			matchedFiles,
			matched,
		)
	}
	return nil
}

func mcpSSEEndpoints(data *httpcodegen.ServiceData) []*httpcodegen.EndpointData {
	endpoints := make([]*httpcodegen.EndpointData, 0)
	for _, endpoint := range data.Endpoints {
		if endpoint != nil && endpoint.SSE != nil {
			endpoints = append(endpoints, endpoint)
		}
	}
	return endpoints
}

func isJSONRPCServerFile(file *codegen.File, name string) bool {
	if file == nil {
		return false
	}
	path := filepath.ToSlash(file.Path)
	return filepath.Base(filepath.Dir(path)) == "server" && filepath.Base(path) == name
}

func mcpJSONRPCSSEServerStreamSection(endpoint *httpcodegen.EndpointData) codegen.Section {
	return codegen.NewJenniferSection(jsonrpcSSEServerStreamSectionName, func(stmt *jen.Statement) {
		emitMCPJSONRPCSSEStreamType(stmt, endpoint)
		emitMCPJSONRPCSSEOpen(stmt, endpoint)
		emitMCPJSONRPCSSESendComment(stmt, endpoint)
		emitMCPJSONRPCSSESend(stmt, endpoint)
		emitMCPJSONRPCSSESendAndClose(stmt, endpoint)
		emitMCPJSONRPCSSESendError(stmt, endpoint)
		emitMCPJSONRPCSSESendEvent(stmt, endpoint)
	})
}

func emitMCPJSONRPCSSEStreamType(stmt *jen.Statement, endpoint *httpcodegen.EndpointData) {
	codegen.Doc(stmt, fmt.Sprintf("%s implements the %s.%s interface using Server-Sent Events.", endpoint.SSE.StructName, endpoint.ServicePkgName, endpoint.Method.ServerStream.Interface))
	stmt.Type().Id(endpoint.SSE.StructName).Struct(
		jen.Id("writer").Op("*").Id("loomhttp").Dot("SSEStreamWriter"),
		jen.Id("encoder").Func().Params(jen.Qual("context", "Context"), jen.Qual("net/http", "ResponseWriter")).Id("loomhttp").Dot("Encoder"),
		jen.Id("w").Qual("net/http", "ResponseWriter"),
		jen.Id("r").Op("*").Qual("net/http", "Request"),
		jen.Id("requestID").Any(),
		jen.Id("requestHasID").Bool(),
		jen.Id("closed").Bool(),
		jen.Id("mu").Qual("sync", "Mutex"),
	)
	stmt.Line()
}

func emitMCPJSONRPCSSEOpen(stmt *jen.Statement, endpoint *httpcodegen.EndpointData) {
	codegen.Doc(stmt, "Open commits the SSE response with an MCP reconnect cursor before the first application event.")
	stmt.Func().Params(jen.Id("s").Op("*").Id(endpoint.SSE.StructName)).Id("Open").
		Params(jen.Id("ctx").Qual("context", "Context")).Error().
		Block(
			jen.Return(jen.Id("s").Dot("writer").Dot("WriteEvent").Call(
				jen.Id("ctx"),
				jen.Func().Params(jen.Id("w").Qual("io", "Writer")).Error().Block(
					jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Qual("fmt", "Fprintf").Call(
						jen.Id("w"),
						jen.Lit("id: %s\ndata:\n\n"),
						jen.Id("mcpruntime").Dot("NewSessionID").Call(),
					),
					jen.Return(jen.Id("err")),
				),
			)),
		)
	stmt.Line()
}

func emitMCPJSONRPCSSESendComment(stmt *jen.Statement, endpoint *httpcodegen.EndpointData) {
	codegen.Doc(stmt, "SendComment writes and flushes an SSE heartbeat comment.")
	stmt.Func().Params(jen.Id("s").Op("*").Id(endpoint.SSE.StructName)).Id("SendComment").
		Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("text").String()).Error().
		Block(jen.Return(jen.Id("s").Dot("writer").Dot("SendComment").Call(jen.Id("ctx"), jen.Id("text"))))
	stmt.Line()
}

func emitMCPJSONRPCSSESend(stmt *jen.Statement, endpoint *httpcodegen.EndpointData) {
	codegen.Doc(stmt, "Send emits a JSON-RPC notification for one stream event.")
	stmt.Func().Params(jen.Id("s").Op("*").Id(endpoint.SSE.StructName)).Id("Send").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("event").Id(endpoint.ServicePkgName).Dot(endpoint.Method.VarName+"Event"),
		).Error().BlockFunc(func(g *jen.Group) {
		emitMCPJSONRPCSSEClosedCheck(g, false)
		emitMCPJSONRPCSSEResultAssertion(g, endpoint)
		emitMCPJSONRPCSSEResultBody(g, endpoint)
		g.Id("message").Op(":=").Map(jen.String()).Any().Values(jen.Dict{
			jen.Lit("jsonrpc"): jen.Lit("2.0"),
			jen.Lit("method"):  jen.Lit(mcpSSENotificationMethod(endpoint)),
			jen.Lit("params"):  jen.Id("body"),
		})
		g.Return(jen.Id("s").Dot("sendSSEEvent").Call(jen.Lit("message"), jen.Id("message")))
	})
	stmt.Line()
}

func emitMCPJSONRPCSSESendAndClose(stmt *jen.Statement, endpoint *httpcodegen.EndpointData) {
	codegen.Doc(stmt, "SendAndClose emits the final JSON-RPC response and closes the stream; ID-less streams suppress the response.")
	stmt.Func().Params(jen.Id("s").Op("*").Id(endpoint.SSE.StructName)).Id("SendAndClose").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("event").Id(endpoint.ServicePkgName).Dot(endpoint.Method.VarName+"Event"),
		).Error().BlockFunc(func(g *jen.Group) {
		emitMCPJSONRPCSSEClosedCheck(g, true)
		emitMCPJSONRPCSSEResultAssertion(g, endpoint)
		g.Id("id").Op(":=").Id("s").Dot("requestID")
		g.If(jen.Op("!").Id("s").Dot("requestHasID")).Block(
			jen.Id("loomtransport").Dot("Observe").Call(
				jen.Id("s").Dot("r").Dot("Context").Call(),
				jen.Id("loomtransport").Dot("Event").Values(jen.Dict{
					jen.Id("Kind"):      jen.Id("loomtransport").Dot("EventKindStreamClose"),
					jen.Id("Reason"):    jen.Id("loomtransport").Dot("ReasonStreamFinalResponseSuppressed"),
					jen.Id("Transport"): jen.Id("loomtransport").Dot("TransportJSONRPC"),
				}),
			),
			jen.Return(jen.Nil()),
		)
		emitMCPJSONRPCSSEResultBody(g, endpoint)
		g.Id("message").Op(":=").Map(jen.String()).Any().Values(jen.Dict{
			jen.Lit("jsonrpc"): jen.Lit("2.0"),
			jen.Lit("id"):      jen.Id("id"),
			jen.Lit("result"):  jen.Id("body"),
		})
		g.Return(jen.Id("s").Dot("sendSSEEvent").Call(jen.Lit("message"), jen.Id("message")))
	})
	stmt.Line()
}

func emitMCPJSONRPCSSEClosedCheck(g *jen.Group, closeStream bool) {
	g.Id("s").Dot("mu").Dot("Lock").Call()
	message := "stream closed"
	if closeStream {
		message = "stream already closed"
	}
	g.If(jen.Id("s").Dot("closed")).Block(
		jen.Id("s").Dot("mu").Dot("Unlock").Call(),
		jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit(message))),
	)
	if closeStream {
		g.Id("s").Dot("closed").Op("=").True()
	}
	g.Id("s").Dot("mu").Dot("Unlock").Call()
}

func emitMCPJSONRPCSSEResultAssertion(g *jen.Group, endpoint *httpcodegen.EndpointData) {
	g.List(jen.Id("result"), jen.Id("ok")).Op(":=").Id("event").Assert(codegen.TypeRef(endpoint.SSE.EventTypeRef))
	g.If(jen.Op("!").Id("ok")).Block(
		jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected event type: %T"), jen.Id("event"))),
	)
}

func emitMCPJSONRPCSSEResultBody(g *jen.Group, endpoint *httpcodegen.EndpointData) {
	if endpoint.Result != nil && len(endpoint.Result.Responses) > 0 {
		response := endpoint.Result.Responses[0]
		if response != nil && len(response.ServerBody) > 0 && response.ServerBody[0].Init != nil {
			g.Id("body").Op(":=").Id(response.ServerBody[0].Init.Name).Call(jen.Id("result"))
			return
		}
	}
	g.Id("body").Op(":=").Id("result")
}

func mcpSSENotificationMethod(endpoint *httpcodegen.EndpointData) string {
	if endpoint.SSE.NotificationMethod != "" {
		return endpoint.SSE.NotificationMethod
	}
	return endpoint.ServiceName + "/stream.event"
}

func emitMCPJSONRPCSSESendError(stmt *jen.Statement, endpoint *httpcodegen.EndpointData) {
	codegen.Doc(stmt, "SendError emits a client-safe JSON-RPC error response.")
	stmt.Func().Params(jen.Id("s").Op("*").Id(endpoint.SSE.StructName)).Id("SendError").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("id").Any(),
			jen.Id("err").Error(),
		).Error().BlockFunc(func(g *jen.Group) {
		g.Id("code").Op(":=").Id("jsonrpc").Dot("InternalError")
		g.Var().Id("serviceError").Op("*").Id("loom").Dot("ServiceError")
		g.If(jen.Qual("errors", "As").Call(jen.Id("err"), jen.Op("&").Id("serviceError"))).Block(
			jen.Id("code").Op("=").Id("jsonrpcErrorCodeForServiceError").Call(jen.Id("serviceError")),
			jen.If(jen.Id("serviceError").Dot("Name").Op("==").Lit("resource_not_found")).Block(
				jen.Id("code").Op("=").Id("jsonrpc").Dot("Code").Call(jen.Lit(-32002)),
			),
		)
		g.Return(jen.Id("s").Dot("sendError").Call(
			jen.Id("ctx"),
			jen.Id("id"),
			jen.Id("code"),
			jen.Id("loom").Dot("ErrorSafeMessage").Call(jen.Id("err")),
			jen.Id("mcpruntime").Dot("NewErrorData").Call(jen.Id("err")),
		))
	})
	stmt.Line()
	codegen.Doc(stmt, "sendError emits a JSON-RPC error response via SSE.")
	stmt.Func().Params(jen.Id("s").Op("*").Id(endpoint.SSE.StructName)).Id("sendError").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("id").Any(),
			jen.Id("code").Id("jsonrpc").Dot("Code"),
			jen.Id("message").String(),
			jen.Id("data").Any(),
		).Error().Block(
		jen.Id("response").Op(":=").Id("jsonrpc").Dot("MakeErrorResponse").Call(jen.Id("id"), jen.Id("code"), jen.Id("message"), jen.Id("data")),
		jen.Return(jen.Id("s").Dot("sendSSEEvent").Call(jen.Lit("message"), jen.Id("response"))),
	)
	stmt.Line()
}

func emitMCPJSONRPCSSESendEvent(stmt *jen.Statement, endpoint *httpcodegen.EndpointData) {
	codegen.Doc(stmt, "sendSSEEvent emits an MCP reconnect hint followed by one JSON SSE event.")
	stmt.Func().Params(jen.Id("s").Op("*").Id(endpoint.SSE.StructName)).Id("sendSSEEvent").
		Params(jen.Id("eventType").String(), jen.Id("v").Any()).Error().
		Block(
			jen.Return(jen.Id("s").Dot("writer").Dot("WriteEvent").Call(
				jen.Id("s").Dot("r").Dot("Context").Call(),
				jen.Func().Params(jen.Id("w").Qual("io", "Writer")).Error().Block(
					jen.If(
						jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Qual("fmt", "Fprint").Call(jen.Id("w"), jen.Lit("event: retry\nretry: 1000\ndata:\n\n")),
						jen.Id("err").Op("!=").Nil(),
					).Block(jen.Return(jen.Id("err"))),
					jen.Return(jen.Id("loomhttp").Dot("WriteJSONSSEEvent").Call(
						jen.Id("w"),
						jen.Id("loomhttp").Dot("SSEMessage").Values(jen.Dict{jen.Id("Type"): jen.Id("eventType")}),
						jen.Id("v"),
					)),
				),
			)),
		)
	stmt.Line()
}
