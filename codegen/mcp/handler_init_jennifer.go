package codegen

import (
	"fmt"
	"path/filepath"

	"github.com/CaliLuke/loom/codegen"
	httpcodegen "github.com/CaliLuke/loom/http/codegen"
	"github.com/dave/jennifer/jen"
)

const jsonrpcServerHandlerInitSectionName = "jsonrpc-server-handler-init"

// replaceMCPJSONRPCHandlerInitSections replaces every upstream JSON-RPC
// handler initializer with an MCP-owned Jennifer section. Loom emits these
// sections in ServiceData.Endpoints order; exact cardinality protects that
// ordering contract from silent upstream drift.
func replaceMCPJSONRPCHandlerInitSections(files []*codegen.File, data *httpcodegen.ServiceData) error {
	if data == nil {
		return fmt.Errorf("MCP JSON-RPC handler initialization requires service data")
	}

	serverFiles := make([]*codegen.File, 0, 1)
	for _, file := range files {
		if file == nil || filepath.Base(filepath.Dir(filepath.ToSlash(file.Path))) != "server" || filepath.Base(file.Path) != "server.go" {
			continue
		}
		serverFiles = append(serverFiles, file)
	}
	if len(serverFiles) != 1 {
		return fmt.Errorf("upstream JSON-RPC handler initialization contract changed: expected one server.go file, found %d", len(serverFiles))
	}

	file := serverFiles[0]
	sections := file.AllSections()
	matches := 0
	for _, section := range sections {
		if section.SectionName() == jsonrpcServerHandlerInitSectionName {
			matches++
		}
	}
	if matches != len(data.Endpoints) {
		return fmt.Errorf(
			"upstream JSON-RPC handler initialization contract changed in %s: expected %d %q sections, found %d",
			filepath.ToSlash(file.Path),
			len(data.Endpoints),
			jsonrpcServerHandlerInitSectionName,
			matches,
		)
	}

	updated := make([]codegen.Section, 0, len(sections))
	endpointIndex := 0
	for _, section := range sections {
		if section.SectionName() != jsonrpcServerHandlerInitSectionName {
			updated = append(updated, section)
			continue
		}
		updated = append(updated, mcpJSONRPCHandlerInitSection(data.Endpoints[endpointIndex]))
		endpointIndex++
	}
	file.SetSections(updated)
	return nil
}

func mcpJSONRPCHandlerInitSection(endpoint *httpcodegen.EndpointData) codegen.Section {
	return codegen.NewJenniferSection(jsonrpcServerHandlerInitSectionName, func(stmt *jen.Statement) {
		comment := fmt.Sprintf(
			"%s creates a JSON-RPC handler which calls the %q service %q endpoint.",
			endpoint.HandlerInit,
			endpoint.ServiceName,
			endpoint.Method.Name,
		)
		codegen.Doc(stmt, comment)
		stmt.Func().Id(endpoint.HandlerInit).
			Params(mcpJSONRPCHandlerInitParams(endpoint)...).
			Add(mcpJSONRPCHandlerInitType()).
			BlockFunc(func(g *jen.Group) {
				if endpoint.Payload != nil && endpoint.Payload.Ref != "" {
					g.Id("decodeParams").Op(":=").Id(endpoint.RequestDecoder).Call(jen.Id("mux"), jen.Id("decoder"))
				}
				if mcpNeedsJSONRPCResponseCapture(endpoint) {
					g.Id("encodeResponse").Op(":=").Id(endpoint.ResponseEncoder).Call(jen.Id("encoder"))
				}
				g.Return(
					jen.Func().Params(mcpJSONRPCHandlerClosureParams()...).Error().BlockFunc(func(body *jen.Group) {
						body.Id("ctx").Op("=").Qual("context", "WithValue").Call(jen.Id("ctx"), codegen.Expr("loom.MethodKey"), jen.Lit(endpoint.Method.Name))
						body.Id("ctx").Op("=").Qual("context", "WithValue").Call(jen.Id("ctx"), codegen.Expr("loom.ServiceKey"), jen.Lit(endpoint.ServiceName))
						body.Line()
						if httpcodegen.IsSSEEndpoint(endpoint) {
							mcpWriteJSONRPCSSEHandlerBody(body, endpoint)
							return
						}
						mcpWriteJSONRPCStandardHandlerBody(body, endpoint)
					}),
				)
			})
	})
}

func mcpJSONRPCHandlerInitParams(endpoint *httpcodegen.EndpointData) []jen.Code {
	params := []jen.Code{
		jen.Id("endpoint").Add(codegen.TypeRef("loom.Endpoint")),
		jen.Id("mux").Add(codegen.TypeRef("loomhttp.Muxer")),
		jen.Id("decoder").Func().Params(jen.Op("*").Qual("net/http", "Request")).Add(codegen.TypeRef("loomhttp.Decoder")),
		jen.Id("encoder").Func().Params(jen.Qual("context", "Context"), jen.Qual("net/http", "ResponseWriter")).Add(codegen.TypeRef("loomhttp.Encoder")),
		jen.Id("errhandler").Func().Params(jen.Qual("context", "Context"), jen.Qual("net/http", "ResponseWriter"), jen.Error()),
	}
	if httpcodegen.IsSSEEndpoint(endpoint) {
		params = append(params, jen.Id("streamWritePolicy").Add(codegen.TypeRef("loomhttp.StreamWritePolicy")))
	}
	return params
}

func mcpJSONRPCHandlerInitType() *jen.Statement {
	return jen.Func().Params(mcpJSONRPCHandlerClosureParams()...).Error()
}

func mcpJSONRPCHandlerClosureParams() []jen.Code {
	return []jen.Code{
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("r").Op("*").Qual("net/http", "Request"),
		jen.Id("req").Op("*").Qual("github.com/CaliLuke/loom/jsonrpc", "RawRequest"),
		jen.Id("w").Qual("net/http", "ResponseWriter"),
	}
}

func mcpWriteJSONRPCStandardHandlerBody(g *jen.Group, endpoint *httpcodegen.EndpointData) {
	if endpoint.Payload != nil && endpoint.Payload.Ref != "" {
		mcpWriteJSONRPCParamsDecode(g, "")
		mcpWriteJSONRPCPayloadIDInjection(g, endpoint.Payload)
	}

	callArgs := []jen.Code{jen.Id("ctx")}
	if endpoint.Payload != nil && endpoint.Payload.Ref != "" {
		callArgs = append(callArgs, jen.Id("params"))
	} else {
		callArgs = append(callArgs, jen.Nil())
	}
	if endpoint.Result == nil || endpoint.Result.Ref == "" {
		switch {
		case mcpNeedsJSONRPCResponseCapture(endpoint):
			g.List(jen.Id("res"), jen.Id("err")).Op(":=").Id("endpoint").Call(callArgs...)
		case endpoint.Payload != nil && endpoint.Payload.Ref != "":
			g.List(jen.Id("_"), jen.Id("err")).Op("=").Id("endpoint").Call(callArgs...)
		default:
			g.List(jen.Id("_"), jen.Id("err")).Op(":=").Id("endpoint").Call(callArgs...)
		}
	} else {
		g.List(jen.Id("res"), jen.Id("err")).Op(":=").Id("endpoint").Call(callArgs...)
	}

	mcpWriteJSONRPCEndpointErrorHandling(g, endpoint, "")
	if endpoint.Result == nil || endpoint.Result.Ref == "" {
		mcpWriteJSONRPCEmptyResultSuccess(g, endpoint)
		return
	}
	mcpWriteJSONRPCResultSuccess(g, endpoint)
}

func mcpWriteJSONRPCParamsDecode(g *jen.Group, streamVar string) {
	g.List(jen.Id("params"), jen.Id("err")).Op(":=").Id("decodeParams").Call(jen.Id("r"), jen.Id("req"))
	g.If(jen.Id("err").Op("!=").Nil()).BlockFunc(func(errors *jen.Group) {
		errors.Id("loomtransport").Dot("RequestObserverFromContext").Call(jen.Id("ctx")).Dot("Fail").Call(jen.Id("loomtransport").Dot("ReasonInvalidJSONRPCParams"))
		errors.If(jen.Id("req").Dot("HasID")).BlockFunc(func(withID *jen.Group) {
			mcpWriteJSONRPCServiceErrorCode(withID)
			if streamVar == "" {
				withID.Id("encodeJSONRPCError").Call(
					jen.Id("ctx"), jen.Id("w"), jen.Id("req"), jen.Id("code"),
					codegen.Expr("loom.ErrorSafeMessage(err)"), codegen.Expr("mcpruntime.NewErrorData(err)"),
					jen.Id("encoder"), jen.Id("errhandler"),
				)
				return
			}
			withID.Return(jen.Id(streamVar).Dot("sendError").Call(
				jen.Id("ctx"), jen.Id("req").Dot("ID"), jen.Id("code"),
				codegen.Expr("loom.ErrorSafeMessage(err)"), codegen.Expr("mcpruntime.NewErrorData(err)"),
			))
		}).Else().BlockFunc(func(withoutID *jen.Group) {
			if streamVar == "" {
				withoutID.Id("errhandler").Call(jen.Id("ctx"), jen.Id("w"), jen.Qual("fmt", "Errorf").Call(jen.Lit("failed to decode parameters: %w"), jen.Id("err")))
			}
		})
		errors.Return(jen.Nil())
	})
}

func mcpWriteJSONRPCEndpointErrorHandling(g *jen.Group, endpoint *httpcodegen.EndpointData, streamVar string) {
	g.If(jen.Id("err").Op("!=").Nil()).BlockFunc(func(errors *jen.Group) {
		errors.Id("loomtransport").Dot("RequestObserverFromContext").Call(jen.Id("ctx")).Dot("Fail").Call(jen.Id("loomtransport").Dot("ReasonHandlerError"))
		errors.If(jen.Id("req").Dot("HasID")).BlockFunc(func(withID *jen.Group) {
			withID.Var().Id("en").Add(codegen.TypeRef("loom.LoomErrorNamer"))
			withID.If(jen.Op("!").Qual("errors", "As").Call(jen.Id("err"), jen.Op("&").Id("en"))).BlockFunc(func(unnamed *jen.Group) {
				mcpWriteJSONRPCServiceErrorCode(unnamed)
				mcpWriteJSONRPCErrorCall(unnamed, streamVar, jen.Id("code"))
				unnamed.Return(jen.Nil())
			})
			withID.Switch(jen.Id("en").Dot("LoomErrorName").Call()).BlockFunc(func(cases *jen.Group) {
				for _, group := range endpoint.Errors {
					for _, item := range group.Errors {
						if item.Response == nil {
							continue
						}
						cases.Case(jen.Lit(item.Name)).BlockFunc(func(itemCase *jen.Group) {
							mcpWriteJSONRPCErrorCall(itemCase, streamVar, jen.Lit(item.Response.Code))
						})
					}
				}
				cases.Case(jen.Lit("invalid_params")).BlockFunc(func(itemCase *jen.Group) {
					mcpWriteJSONRPCErrorCall(itemCase, streamVar, jen.Qual("github.com/CaliLuke/loom/jsonrpc", "InvalidParams"))
				})
				cases.Case(jen.Lit("resource_not_found")).BlockFunc(func(itemCase *jen.Group) {
					mcpWriteJSONRPCErrorCall(itemCase, streamVar, jen.Qual("github.com/CaliLuke/loom/jsonrpc", "Code").Call(jen.Lit(-32002)))
				})
				cases.Case(jen.Lit("method_not_found")).BlockFunc(func(itemCase *jen.Group) {
					mcpWriteJSONRPCErrorCall(itemCase, streamVar, jen.Qual("github.com/CaliLuke/loom/jsonrpc", "MethodNotFound"))
				})
				cases.Default().BlockFunc(func(itemCase *jen.Group) {
					mcpWriteJSONRPCServiceErrorCode(itemCase)
					mcpWriteJSONRPCErrorCall(itemCase, streamVar, jen.Id("code"))
				})
			})
		}).Else().BlockFunc(func(withoutID *jen.Group) {
			if streamVar == "" {
				withoutID.Id("errhandler").Call(jen.Id("ctx"), jen.Id("w"), jen.Qual("fmt", "Errorf").Call(jen.Lit("endpoint error: %w"), jen.Id("err")))
			}
		})
		errors.Return(jen.Nil())
	})
	g.Line()
}

func mcpWriteJSONRPCServiceErrorCode(g *jen.Group) {
	g.Id("code").Op(":=").Qual("github.com/CaliLuke/loom/jsonrpc", "InternalError")
	g.Var().Id("serviceError").Op("*").Add(codegen.TypeRef("loom.ServiceError"))
	g.If(jen.Qual("errors", "As").Call(jen.Id("err"), jen.Op("&").Id("serviceError"))).Block(
		jen.Id("code").Op("=").Id("jsonrpcErrorCodeForServiceError").Call(jen.Id("serviceError")),
		jen.If(jen.Id("serviceError").Dot("Name").Op("==").Lit("resource_not_found")).Block(
			jen.Id("code").Op("=").Qual("github.com/CaliLuke/loom/jsonrpc", "Code").Call(jen.Lit(-32002)),
		),
	)
}

func mcpWriteJSONRPCErrorCall(g *jen.Group, streamVar string, code jen.Code) {
	if streamVar != "" {
		g.Return(jen.Id(streamVar).Dot("sendError").Call(
			jen.Id("ctx"), jen.Id("req").Dot("ID"), code,
			codegen.Expr("loom.ErrorSafeMessage(err)"), codegen.Expr("mcpruntime.NewErrorData(err)"),
		))
		return
	}
	g.Id("encodeJSONRPCError").Call(
		jen.Id("ctx"), jen.Id("w"), jen.Id("req"), code,
		codegen.Expr("loom.ErrorSafeMessage(err)"), codegen.Expr("mcpruntime.NewErrorData(err)"),
		jen.Id("encoder"), jen.Id("errhandler"),
	)
}

func mcpWriteJSONRPCEmptyResultSuccess(g *jen.Group, endpoint *httpcodegen.EndpointData) {
	g.If(jen.Op("!").Id("req").Dot("HasID")).Block(jen.Return(jen.Nil()))
	if mcpNeedsJSONRPCResponseCapture(endpoint) {
		mcpWriteJSONRPCResponseCapture(g)
	}
	g.Id("response").Op(":=").Qual("github.com/CaliLuke/loom/jsonrpc", "MakeSuccessResponse").Call(jen.Id("req").Dot("ID"), jen.Struct().Values())
	mcpWriteJSONRPCEncodeResponse(g)
	g.Return(jen.Nil())
}

func mcpWriteJSONRPCResultSuccess(g *jen.Group, endpoint *httpcodegen.EndpointData) {
	g.Id("id").Op(":=").Id("req").Dot("ID")
	g.If(jen.Op("!").Id("req").Dot("HasID")).Block(jen.Return(jen.Nil()))
	if mcpNeedsJSONRPCResponseCapture(endpoint) {
		mcpWriteJSONRPCResponseCapture(g)
		g.Var().Id("result").Any()
		g.If(jen.Id("capture").Dot("body").Dot("Len").Call().Op(">").Lit(0)).Block(
			jen.Id("result").Op("=").Qual("encoding/json", "RawMessage").Call(jen.Id("capture").Dot("body").Dot("Bytes").Call()),
		)
		g.Id("response").Op(":=").Qual("github.com/CaliLuke/loom/jsonrpc", "MakeSuccessResponse").Call(jen.Id("id"), jen.Id("result"))
		mcpWriteJSONRPCEncodeResponse(g)
		g.Return(jen.Nil())
		return
	}
	if len(endpoint.Result.Responses) > 0 {
		success := endpoint.Result.Responses[0]
		if success != nil && len(success.ServerBody) > 0 && success.ServerBody[0].Init != nil {
			g.Comment("Convert result to response body with proper JSON tags")
			if endpoint.Method.ViewedResult != nil {
				g.Id("viewedRes").Op(":=").Id("res").Assert(codegen.TypeRef(endpoint.Method.ViewedResult.FullRef))
				g.Id("body").Op(":=").Id(success.ServerBody[0].Init.Name).Call(jen.Id("viewedRes").Dot("Projected"))
			} else {
				g.Id("body").Op(":=").Id(success.ServerBody[0].Init.Name).Call(jen.Id("res").Assert(codegen.TypeRef(endpoint.Result.Ref)))
			}
			g.Id("response").Op(":=").Qual("github.com/CaliLuke/loom/jsonrpc", "MakeSuccessResponse").Call(jen.Id("id"), jen.Id("body"))
			mcpWriteJSONRPCEncodeResponse(g)
			g.Return(jen.Nil())
			return
		}
	}
	g.Id("response").Op(":=").Qual("github.com/CaliLuke/loom/jsonrpc", "MakeSuccessResponse").Call(jen.Id("id"), jen.Id("res"))
	mcpWriteJSONRPCEncodeResponse(g)
	g.Return(jen.Nil())
}

func mcpWriteJSONRPCResponseCapture(g *jen.Group) {
	g.Id("capture").Op(":=").Op("&").Id("jsonrpcResponseCapture").Values()
	g.If(jen.Err().Op(":=").Id("encodeResponse").Call(jen.Id("ctx"), jen.Id("capture"), jen.Id("res")), jen.Err().Op("!=").Nil()).Block(
		jen.Id("loomtransport").Dot("RequestObserverFromContext").Call(jen.Id("ctx")).Dot("Fail").Call(jen.Id("loomtransport").Dot("ReasonResponseWriteFailed")),
		jen.Id("errhandler").Call(jen.Id("ctx"), jen.Id("w"), jen.Qual("fmt", "Errorf").Call(jen.Lit("failed to encode transport response: %w"), jen.Err())),
		jen.Return(jen.Nil()),
	)
	g.Id("copyJSONRPCResponseMetadata").Call(jen.Id("w"), jen.Id("capture"))
}

func mcpWriteJSONRPCEncodeResponse(g *jen.Group) {
	g.If(jen.Err().Op(":=").Id("encoder").Call(jen.Id("ctx"), jen.Id("w")).Dot("Encode").Call(jen.Id("response")), jen.Err().Op("!=").Nil()).Block(
		jen.Id("loomtransport").Dot("RequestObserverFromContext").Call(jen.Id("ctx")).Dot("Fail").Call(jen.Id("loomtransport").Dot("ReasonResponseWriteFailed")),
		jen.Id("errhandler").Call(jen.Id("ctx"), jen.Id("w"), jen.Qual("fmt", "Errorf").Call(jen.Lit("failed to encode JSON-RPC response: %w"), jen.Err())),
	)
}

func mcpWriteJSONRPCPayloadIDInjection(g *jen.Group, payload *httpcodegen.PayloadData) {
	if payload.IDAttribute == "" {
		return
	}
	if payload.IDAttributeRequired {
		g.If(jen.Id("req").Dot("ID").Op("!=").Nil()).Block(
			jen.Id("params").Dot(payload.IDAttribute).Op("=").Qual("github.com/CaliLuke/loom/jsonrpc", "IDToString").Call(jen.Id("req").Dot("ID")),
		)
		return
	}
	g.If(jen.Id("req").Dot("ID").Op("!=").Nil()).Block(
		jen.Id("idStr").Op(":=").Qual("github.com/CaliLuke/loom/jsonrpc", "IDToString").Call(jen.Id("req").Dot("ID")),
		jen.Id("params").Dot(payload.IDAttribute).Op("=").Op("&").Id("idStr"),
	)
}

func mcpWriteJSONRPCSSEHandlerBody(g *jen.Group, endpoint *httpcodegen.EndpointData) {
	g.Id("strm").Op(":=").Op("&").Id(endpoint.SSE.StructName).Values(jen.Dict{
		jen.Id("w"):            jen.Id("w"),
		jen.Id("r"):            jen.Id("r"),
		jen.Id("writer"):       jen.Id("loomhttp").Dot("NewSSEStreamWriter").Call(jen.Id("w"), jen.Id("r").Dot("Context").Call(), jen.Id("loomtransport").Dot("TransportJSONRPC"), jen.Id("streamWritePolicy")),
		jen.Id("encoder"):      jen.Id("encoder"),
		jen.Id("requestID"):    jen.Id("req").Dot("ID"),
		jen.Id("requestHasID"): jen.Id("req").Dot("HasID"),
	})
	if endpoint.Method.Name == "events/stream" {
		g.If(jen.Id("r").Dot("Method").Op("==").Qual("net/http", "MethodGet").Op("&&").Id("req").Dot("Method").Op("==").Lit("events/stream")).Block(
			jen.If(jen.Err().Op(":=").Id("strm").Dot("Open").Call(jen.Id("r").Dot("Context").Call()), jen.Err().Op("!=").Nil()).Block(jen.Return(jen.Err())),
		)
	}
	if endpoint.Payload != nil && endpoint.Payload.Ref != "" {
		mcpWriteJSONRPCParamsDecode(g, "strm")
		mcpWriteJSONRPCPayloadIDInjection(g, endpoint.Payload)
	}
	if endpoint.SSE.RequestIDField != "" {
		g.If(jen.Id("lastEventID").Op(":=").Id("r").Dot("Header").Dot("Get").Call(jen.Lit("Last-Event-ID")), jen.Id("lastEventID").Op("!=").Lit("")).BlockFunc(func(lastEvent *jen.Group) {
			lastEvent.Id("ctx").Op("=").Qual("context", "WithValue").Call(jen.Id("ctx"), codegen.TypeRef("loomhttp.LastEventIDKey"), jen.Id("lastEventID"))
			if endpoint.Payload != nil && endpoint.Payload.Ref != "" && endpoint.Payload.Request != nil && endpoint.Payload.Request.PayloadType != nil && endpoint.Payload.Request.PayloadType.Name() == "Object" {
				lastEvent.Id("params").Dot(endpoint.SSE.RequestIDField).Op("=").Id("lastEventID")
			}
		})
	}
	input := jen.Dict{jen.Id("Stream"): jen.Id("strm")}
	if endpoint.Payload != nil && endpoint.Payload.Ref != "" {
		input[jen.Id("Payload")] = jen.Id("params")
	}
	g.Id("v").Op(":=").Op("&").Qual(endpoint.ServicePkgName, endpoint.Method.ServerStream.EndpointStruct).Values(input)
	g.If(jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id("endpoint").Call(jen.Id("ctx"), jen.Id("v")), jen.Id("err").Op("!=").Nil()).BlockFunc(func(errors *jen.Group) {
		errors.If(jen.Id("req").Dot("HasID")).BlockFunc(func(withID *jen.Group) {
			withID.Var().Id("en").Add(codegen.TypeRef("loom.LoomErrorNamer"))
			withID.If(jen.Qual("errors", "As").Call(jen.Id("err"), jen.Op("&").Id("en"))).BlockFunc(func(named *jen.Group) {
				named.Switch(jen.Id("en").Dot("LoomErrorName").Call()).BlockFunc(func(cases *jen.Group) {
					cases.Case(jen.Lit("invalid_params")).BlockFunc(func(itemCase *jen.Group) {
						mcpWriteJSONRPCErrorCall(itemCase, "strm", jen.Qual("github.com/CaliLuke/loom/jsonrpc", "InvalidParams"))
					})
					cases.Case(jen.Lit("resource_not_found")).BlockFunc(func(itemCase *jen.Group) {
						mcpWriteJSONRPCErrorCall(itemCase, "strm", jen.Qual("github.com/CaliLuke/loom/jsonrpc", "Code").Call(jen.Lit(-32002)))
					})
					cases.Case(jen.Lit("method_not_found")).BlockFunc(func(itemCase *jen.Group) {
						mcpWriteJSONRPCErrorCall(itemCase, "strm", jen.Qual("github.com/CaliLuke/loom/jsonrpc", "MethodNotFound"))
					})
				})
			})
			mcpWriteJSONRPCServiceErrorCode(withID)
			mcpWriteJSONRPCErrorCall(withID, "strm", jen.Id("code"))
		})
		errors.Return(jen.Nil())
	})
	g.Return(jen.Nil())
}

func mcpNeedsJSONRPCResponseCapture(endpoint *httpcodegen.EndpointData) bool {
	if endpoint == nil || endpoint.Result == nil || len(endpoint.Result.Responses) == 0 || endpoint.Result.Responses[0] == nil {
		return false
	}
	response := endpoint.Result.Responses[0]
	return len(response.Headers) > 0 || len(response.Cookies) > 0
}
