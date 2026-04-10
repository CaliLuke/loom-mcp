package codegen

import (
	"github.com/CaliLuke/loom/codegen"
	"github.com/dave/jennifer/jen"
)

func adapterToolsSection(data *AdapterData) codegen.Section {
	return codegen.MustJenniferSection("mcp-adapter-tools", func(stmt *jen.Statement) {
		if len(data.Tools) == 0 {
			return
		}

		stmt.Comment("Tools handling").Line()
		emitDecodeMCPPayloadStrict(stmt)
		emitTopLevelJSONFieldSet(stmt)
		emitDecodeMCPPayloadFields(stmt)
		emitValidateMCPPayloadRequired(stmt)
		emitValidateMCPPayloadEnum(stmt)
		emitToolsList(stmt, data)
		emitToolStreamBridges(stmt, data)
		emitToolsCall(stmt)
		emitToolsCallHandler(stmt, data)
	})
}

func emitDecodeMCPPayloadStrict(stmt *jen.Statement) {
	stmt.Func().Id("decodeMCPPayloadStrict").
		Params(jen.Id("data").Index().Byte(), jen.Id("payload").Any()).
		Error().
		Block(
			jen.Id("dec").Op(":=").Qual("encoding/json", "NewDecoder").Call(jen.Qual("bytes", "NewReader").Call(jen.Id("data"))),
			jen.Id("dec").Dot("DisallowUnknownFields").Call(),
			jen.If(jen.Id("err").Op(":=").Id("dec").Dot("Decode").Call(jen.Id("payload")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Id("err")),
			),
			jen.If(jen.Id("err").Op(":=").Id("dec").Dot("Decode").Call(jen.Op("&").Struct().Values()), jen.Id("err").Op("!=").Qual("io", "EOF")).Block(
				jen.If(jen.Id("err").Op("==").Nil()).Block(
					jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected trailing JSON data"))),
				),
				jen.Return(jen.Id("err")),
			),
			jen.Return(jen.Nil()),
		)
	stmt.Line()
}

func emitTopLevelJSONFieldSet(stmt *jen.Statement) {
	stmt.Func().Id("topLevelJSONFieldSet").
		Params(jen.Id("raw").Qual("encoding/json", "RawMessage")).
		Params(jen.Map(jen.String()).Struct(), jen.Error()).
		Block(
			jen.Id("fields").Op(":=").Make(jen.Map(jen.String()).Struct()),
			jen.If(jen.Len(jen.Qual("bytes", "TrimSpace").Call(jen.Id("raw"))).Op("==").Lit(0)).Block(
				jen.Return(jen.Id("fields"), jen.Nil()),
			),
			jen.Var().Id("payload").Map(jen.String()).Qual("encoding/json", "RawMessage"),
			jen.If(jen.Id("err").Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("raw"), jen.Op("&").Id("payload")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.For(jen.Id("name").Op(":=").Range().Id("payload")).Block(
				jen.Id("fields").Index(jen.Id("name")).Op("=").Struct().Values(),
			),
			jen.Return(jen.Id("fields"), jen.Nil()),
		)
	stmt.Line()
}

func emitDecodeMCPPayloadFields(stmt *jen.Statement) {
	stmt.Func().Id("decodeMCPPayloadFields").
		Params(jen.Id("data").Index().Byte()).
		Params(jen.Map(jen.String()).Qual("encoding/json", "RawMessage"), jen.Error()).
		Block(
			jen.Var().Id("fields").Map(jen.String()).Qual("encoding/json", "RawMessage"),
			jen.If(jen.Id("err").Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("data"), jen.Op("&").Id("fields")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.If(jen.Id("fields").Op("==").Nil()).Block(
				jen.Id("fields").Op("=").Make(jen.Map(jen.String()).Qual("encoding/json", "RawMessage")),
			),
			jen.Return(jen.Id("fields"), jen.Nil()),
		)
	stmt.Line()
}

func emitValidateMCPPayloadRequired(stmt *jen.Statement) {
	stmt.Func().Id("validateMCPPayloadRequired").
		Params(
			jen.Id("fields").Map(jen.String()).Qual("encoding/json", "RawMessage"),
			jen.Id("field").String(),
		).
		Error().
		Block(
			jen.List(jen.Id("raw"), jen.Id("ok")).Op(":=").Id("fields").Index(jen.Id("field")),
			jen.If(jen.Op("!").Id("ok")).Block(
				jen.Return(requiredFieldErrorExpr(jen.Id("field"))),
			),
			jen.Id("trimmed").Op(":=").Qual("bytes", "TrimSpace").Call(jen.Id("raw")),
			jen.If(
				jen.Qual("bytes", "Equal").Call(jen.Id("trimmed"), jen.Index().Byte().Call(jen.Lit(`""`))).Op("||").
					Qual("bytes", "Equal").Call(jen.Id("trimmed"), jen.Index().Byte().Call(jen.Lit("null"))),
			).Block(
				jen.Return(requiredFieldErrorExpr(jen.Id("field"))),
			),
			jen.Return(jen.Nil()),
		)
	stmt.Line()
}

func requiredFieldErrorExpr(field jen.Code) jen.Code {
	return jen.Id("goa").Dot("WithErrorRemedy").Call(
		jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Missing required field: %s"), field),
		jen.Op("&").Id("goa").Dot("ErrorRemedy").Values(jen.Dict{
			jen.Id("Code"):        jen.Lit("invalid_params"),
			jen.Id("SafeMessage"): jen.Qual("fmt", "Sprintf").Call(jen.Lit("Missing required field: %s"), field),
			jen.Id("RetryHint"):   jen.Qual("fmt", "Sprintf").Call(jen.Lit("Include required field %q."), field),
		}),
	)
}

func emitValidateMCPPayloadEnum(stmt *jen.Statement) {
	stmt.Func().Id("validateMCPPayloadEnum").
		Params(
			jen.Id("fields").Map(jen.String()).Qual("encoding/json", "RawMessage"),
			jen.Id("field").String(),
			jen.Id("allowed").Op("...").String(),
		).
		Error().
		Block(
			jen.List(jen.Id("raw"), jen.Id("ok")).Op(":=").Id("fields").Index(jen.Id("field")),
			jen.If(jen.Op("!").Id("ok")).Block(
				jen.Return(jen.Nil()),
			),
			jen.Var().Id("value").Any(),
			jen.If(jen.Id("err").Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("raw"), jen.Op("&").Id("value")), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Id("err")),
			),
			jen.Id("actual").Op(":=").Qual("fmt", "Sprint").Call(jen.Id("value")),
			jen.For(jen.List(jen.Id("_"), jen.Id("candidate")).Op(":=").Range().Id("allowed")).Block(
				jen.If(jen.Id("actual").Op("==").Id("candidate")).Block(
					jen.Return(jen.Nil()),
				),
			),
			jen.Return(
				jen.Id("goa").Dot("WithErrorRemedy").Call(
					jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Invalid value for %s"), jen.Id("field")),
					jen.Op("&").Id("goa").Dot("ErrorRemedy").Values(jen.Dict{
						jen.Id("Code"):        jen.Lit("invalid_params"),
						jen.Id("SafeMessage"): jen.Qual("fmt", "Sprintf").Call(jen.Lit("Invalid value for %s"), jen.Id("field")),
						jen.Id("RetryHint"):   jen.Qual("fmt", "Sprintf").Call(jen.Lit("Use one of: %s."), jen.Qual("strings", "Join").Call(jen.Id("allowed"), jen.Lit(", "))),
					}),
				),
			),
		)
	stmt.Line()
}

func emitToolsList(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("ToolsList").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("p").Op("*").Id("ToolsListPayload"),
		).
		Params(
			jen.Id("res").Op("*").Id("ToolsListResult"),
			jen.Id("err").Error(),
		).
		BlockFunc(func(g *jen.Group) {
			g.Id("ctx").Op(",").Id("span").Op(",").Id("start").Op(",").Id("attrs").Op(":=").Id("a").Dot("startTelemetry").Call(jen.Id("ctx"), jen.Lit("tools/list"))
			g.Defer().Func().Params().Block(
				jen.Id("a").Dot("finishTelemetry").Call(jen.Id("ctx"), jen.Id("span"), jen.Id("start"), jen.Id("attrs"), jen.Id("err"), jen.False()),
			).Call()
			g.If(jen.Op("!").Id("a").Dot("isInitialized").Call(jen.Id("ctx"))).Block(
				jen.Return(jen.Nil(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
			)
			g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("request"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"): jen.Lit("tools/list"),
			}))
			g.Id("tools").Op(":=").Index().Op("*").Id("ToolInfo").ValuesFunc(func(vals *jen.Group) {
				for _, tool := range data.Tools {
					dict := jen.Dict{
						jen.Id("Name"):        jen.Lit(tool.Name),
						jen.Id("Description"): jen.Id("stringPtr").Call(jen.Lit(tool.Description)),
					}
					if tool.InputSchema != "" {
						dict[jen.Id("InputSchema")] = jen.Qual("encoding/json", "RawMessage").Call(jen.Index().Byte().Call(jen.Lit(tool.InputSchema)))
					} else {
						dict[jen.Id("InputSchema")] = jen.Qual("encoding/json", "RawMessage").Call(jen.Index().Byte().Call(jen.Lit(`{"type":"object","properties":{},"additionalProperties":false}`)))
					}
					if tool.AnnotationsJSON != "" {
						dict[jen.Id("Annotations")] = jen.Qual("encoding/json", "RawMessage").Call(jen.Index().Byte().Call(jen.Lit(tool.AnnotationsJSON)))
					}
					if icons := iconSliceValue(tool.Icons); icons != nil {
						dict[jen.Id("Icons")] = icons
					}
					vals.Add(jen.Op("&").Id("ToolInfo").Values(dict))
				}
			})
			g.Id("res").Op("=").Op("&").Id("ToolsListResult").Values(jen.Dict{
				jen.Id("Tools"): jen.Id("tools"),
			})
			g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"): jen.Lit("tools/list"),
			}))
			g.Return(jen.Id("res"), jen.Nil())
		})
	stmt.Line()
}

func emitToolStreamBridges(stmt *jen.Statement, data *AdapterData) {
	if !data.ToolsCallStreaming {
		return
	}

	for _, tool := range data.Tools {
		if !tool.IsStreaming {
			continue
		}

		typeName := streamBridgeTypeName(tool)
		eventType := jen.Id(data.Package).Dot(tool.StreamEventType)

		stmt.Type().Id(typeName).Struct(
			jen.Id("out").Id("ToolsCallServerStream"),
			jen.Id("adapter").Op("*").Id("MCPAdapter"),
		)
		stmt.Line()
		emitStreamBridgeSendMethod(stmt, typeName, eventType, "Send")
		stmt.Line()
		emitStreamBridgeSendMethod(stmt, typeName, eventType, "SendAndClose")
		stmt.Line()
		stmt.Func().Params(jen.Id("b").Op("*").Id(typeName)).
			Id("SendError").
			Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("id").String(),
				jen.Id("err").Error(),
			).
			Error().
			Block(
				jen.Return(jen.Id("b").Dot("out").Dot("SendError").Call(jen.Id("ctx"), jen.Id("id"), jen.Id("err"))),
			)
		stmt.Line()
	}
}

func streamBridgeTypeName(tool *ToolAdapter) string {
	return codegen.Goify(tool.OriginalMethodName, true) + "StreamBridge"
}

func emitStreamBridgeSendMethod(stmt *jen.Statement, typeName string, eventType jen.Code, methodName string) {
	stmt.Func().Params(jen.Id("b").Op("*").Id(typeName)).
		Id(methodName).
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("ev").Add(eventType),
		).
		Error().
		Block(
			jen.List(jen.Id("s"), jen.Id("e")).Op(":=").Id("mcpruntime").Dot("EncodeJSONToString").Call(jen.Id("ctx"), jen.Id("goahttp").Dot("ResponseEncoder"), jen.Id("ev")),
			jen.If(jen.Id("e").Op("!=").Nil()).Block(
				jen.Return(jen.Id("e")),
			),
			jen.Return(jen.Id("b").Dot("out").Dot(methodName).Call(jen.Id("ctx"), jen.Op("&").Id("ToolsCallResult").Values(jen.Dict{
				jen.Id("Content"): jen.Index().Op("*").Id("ContentItem").Values(
					jen.Id("buildContentItem").Call(jen.Id("b").Dot("adapter"), jen.Id("s")),
				),
			}))),
		)
}

func emitToolsCall(stmt *jen.Statement) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("ToolsCall").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("p").Op("*").Id("ToolsCallPayload"),
			jen.Id("stream").Id("ToolsCallServerStream"),
		).
		Params(jen.Id("err").Error()).
		Block(
			jen.Id("attrs").Op(":=").Index().Qual("go.opentelemetry.io/otel/attribute", "KeyValue").Values(),
			jen.If(jen.Id("p").Op("!=").Nil().Op("&&").Id("p").Dot("Name").Op("!=").Lit("")).Block(
				jen.Id("attrs").Op("=").Append(
					jen.Id("attrs"),
					jen.Qual("go.opentelemetry.io/otel/attribute", "String").Call(jen.Lit("mcp.tool"), jen.Id("p").Dot("Name")),
					jen.Qual("go.opentelemetry.io/otel/attribute", "String").Call(jen.Lit("tool"), jen.Id("p").Dot("Name")),
				),
			),
			jen.Id("ctx").Op(",").Id("span").Op(",").Id("start").Op(",").Id("attrs").Op(":=").Id("a").Dot("startTelemetry").Call(jen.Id("ctx"), jen.Lit("tools/call"), jen.Id("attrs").Op("...")),
			jen.Id("toolErr").Op(":=").False(),
			jen.Defer().Func().Params().Block(
				jen.Id("a").Dot("finishTelemetry").Call(jen.Id("ctx"), jen.Id("span"), jen.Id("start"), jen.Id("attrs"), jen.Id("err"), jen.Id("toolErr")),
			).Call(),
			jen.Id("info").Op(":=").Id("a").Dot("toolCallInfo").Call(jen.Id("p")),
			jen.Id("handler").Op(":=").Id("a").Dot("wrapToolCallHandler").Call(jen.Id("info"), jen.Id("a").Dot("toolsCallHandler")),
			jen.Id("toolErr").Op(",").Id("err").Op("=").Id("handler").Call(jen.Id("ctx"), jen.Id("p"), jen.Id("stream")),
			jen.Return(jen.Id("err")),
		)
	stmt.Line()
}

func emitToolsCallHandler(stmt *jen.Statement, data *AdapterData) {
	stmt.Func().Params(jen.Id("a").Op("*").Id("MCPAdapter")).
		Id("toolsCallHandler").
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("p").Op("*").Id("ToolsCallPayload"),
			jen.Id("stream").Id("ToolsCallServerStream"),
		).
		Params(jen.Bool(), jen.Error()).
		BlockFunc(func(g *jen.Group) {
			g.If(jen.Op("!").Id("a").Dot("isInitialized").Call(jen.Id("ctx"))).Block(
				jen.Return(jen.False(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("invalid_params"), jen.Lit("Not initialized"))),
			)
			g.Id("name").Op(":=").Lit("")
			g.If(jen.Id("p").Op("!=").Nil()).Block(
				jen.Id("name").Op("=").Id("p").Dot("Name"),
			)
			g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("request"), jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("method"): jen.Lit("tools/call"),
				jen.Lit("name"):   jen.Id("name"),
			}))
			g.Switch(jen.Id("p").Dot("Name")).BlockFunc(func(sw *jen.Group) {
				for _, tool := range data.Tools {
					sw.Case(jen.Lit(tool.Name)).BlockFunc(func(caseg *jen.Group) {
						emitToolCase(caseg, tool)
					})
				}
				sw.Default().Block(
					jen.Return(jen.False(), jen.Id("goa").Dot("PermanentError").Call(jen.Lit("method_not_found"), jen.Lit("Unknown tool: %s"), jen.Id("p").Dot("Name"))),
				)
			})
		})
	stmt.Line()
}

func emitToolCase(g *jen.Group, tool *ToolAdapter) {
	if tool.HasPayload {
		g.Var().Id("payload").Add(rawExpr(tool.PayloadType))
		if len(tool.DefaultFields) > 0 || len(tool.RequiredFields) > 0 || len(tool.EnumFields) > 0 {
			g.List(jen.Id("fields"), jen.Id("ferr")).Op(":=").Id("topLevelJSONFieldSet").Call(jen.Id("p").Dot("Arguments"))
			g.If(jen.Id("ferr").Op("!=").Nil()).Block(
				jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolInputError").Call(jen.Id("ferr"), jen.Id("p").Dot("Arguments")))),
			)
			g.List(jen.Id("rawFields"), jen.Id("err")).Op(":=").Id("decodeMCPPayloadFields").Call(jen.Id("p").Dot("Arguments"))
			g.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolInputError").Call(jen.Id("err"), jen.Id("p").Dot("Arguments")))),
			)
			g.Id("_").Op("=").Id("fields")
			g.Id("_").Op("=").Id("rawFields")
		}
		g.If(jen.Id("err").Op(":=").Id("decodeMCPPayloadStrict").Call(jen.Id("p").Dot("Arguments"), jen.Op("&").Id("payload")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolInputError").Call(jen.Id("err"), jen.Id("p").Dot("Arguments")))),
		)
		emitToolDefaultAssignments(g, tool)
		emitToolRequiredChecks(g, tool)
		emitToolEnumChecks(g, tool)
	}

	if tool.IsStreaming {
		g.Id("bridge").Op(":=").Op("&").Id(streamBridgeTypeName(tool)).Values(jen.Dict{
			jen.Id("out"):     jen.Id("stream"),
			jen.Id("adapter"): jen.Id("a"),
		})
		call := serviceMethodCall(tool, jen.Id("a").Dot("service"), jen.Id("ctx"), tool.HasPayload, jen.Id("payload"), true, jen.Id("bridge"))
		g.If(jen.Id("err").Op(":=").Add(call), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("err"))),
		)
		g.Return(jen.False(), jen.Nil())
		return
	}

	if tool.HasResult {
		call := serviceMethodCall(tool, jen.Id("a").Dot("service"), jen.Id("ctx"), tool.HasPayload, jen.Id("payload"), false, nil)
		g.List(jen.Id("result"), jen.Id("err")).Op(":=").Add(call)
		g.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("err"))),
		)
		if tool.ResultType == "string" {
			g.Id("s").Op(":=").String().Call(jen.Id("result"))
			g.Id("structuredContent").Op(":=").Qual("encoding/json", "RawMessage").Call(jen.Nil())
		} else {
			g.Id("s").Op(":=").Id("formatToolSuccessText").Call(jen.Id("result"))
			g.List(jen.Id("structuredContent"), jen.Id("serr")).Op(":=").Qual("encoding/json", "Marshal").Call(jen.Id("result"))
			g.If(jen.Id("serr").Op("!=").Nil()).Block(
				jen.Return(jen.False(), jen.Id("serr")),
			)
		}
		g.Id("final").Op(":=").Op("&").Id("ToolsCallResult").Values(jen.Dict{
			jen.Id("Content"): jen.Index().Op("*").Id("ContentItem").Values(
				jen.Id("buildContentItem").Call(jen.Id("a"), jen.Id("s")),
			),
			jen.Id("StructuredContent"): jen.Id("structuredContent"),
		})
		g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
			jen.Lit("method"): jen.Lit("tools/call"),
			jen.Lit("name"):   jen.Id("p").Dot("Name"),
		}))
		g.Return(jen.False(), jen.Id("stream").Dot("SendAndClose").Call(jen.Id("ctx"), jen.Id("final")))
		return
	}

	call := serviceMethodCall(tool, jen.Id("a").Dot("service"), jen.Id("ctx"), tool.HasPayload, jen.Id("payload"), false, nil)
	g.If(jen.Id("err").Op(":=").Add(call), jen.Id("err").Op("!=").Nil()).Block(
		jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("err"))),
	)
	g.Id("ok").Op(":=").Id("stringPtr").Call(jen.Lit("OK"))
	g.Id("a").Dot("log").Call(jen.Id("ctx"), jen.Lit("response"), jen.Map(jen.String()).Any().Values(jen.Dict{
		jen.Lit("method"): jen.Lit("tools/call"),
		jen.Lit("name"):   jen.Id("p").Dot("Name"),
	}))
	g.Return(jen.False(), jen.Id("stream").Dot("SendAndClose").Call(jen.Id("ctx"), jen.Op("&").Id("ToolsCallResult").Values(jen.Dict{
		jen.Id("Content"): jen.Index().Op("*").Id("ContentItem").Values(
			jen.Op("&").Id("ContentItem").Values(jen.Dict{
				jen.Id("Type"): jen.Lit("text"),
				jen.Id("Text"): jen.Id("ok"),
			}),
		),
	})))
}

func emitToolDefaultAssignments(g *jen.Group, tool *ToolAdapter) {
	if len(tool.DefaultFields) == 0 {
		return
	}
	g.BlockFunc(func(block *jen.Group) {
		for _, field := range tool.DefaultFields {
			block.If(jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("fields").Index(jen.Lit(field.Name)), jen.Op("!").Id("ok")).Block(
				jen.Id("payload").Dot(field.GoName).Op("=").Add(rawExpr(field.Literal)),
			)
		}
	})
}

func emitToolRequiredChecks(g *jen.Group, tool *ToolAdapter) {
	if len(tool.RequiredFields) == 0 {
		return
	}
	g.BlockFunc(func(block *jen.Group) {
		for _, field := range tool.RequiredFields {
			block.If(jen.Id("err").Op(":=").Id("validateMCPPayloadRequired").Call(jen.Id("rawFields"), jen.Lit(field)), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolInputError").Call(jen.Id("err"), jen.Id("p").Dot("Arguments")))),
			)
		}
	})
}

func emitToolEnumChecks(g *jen.Group, tool *ToolAdapter) {
	if len(tool.EnumFields) == 0 {
		return
	}
	g.BlockFunc(func(block *jen.Group) {
		for field, vals := range tool.EnumFields {
			args := make([]jen.Code, 0, 2+len(vals))
			args = append(args, jen.Id("rawFields"), jen.Lit(field))
			for _, val := range vals {
				args = append(args, jen.Lit(val))
			}
			block.If(jen.Id("err").Op(":=").Id("validateMCPPayloadEnum").Call(args...), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.True(), jen.Id("a").Dot("sendToolError").Call(jen.Id("ctx"), jen.Id("stream"), jen.Id("p").Dot("Name"), jen.Id("toolInputError").Call(jen.Id("err"), jen.Id("p").Dot("Arguments")))),
			)
		}
	})
}

func serviceMethodCall(tool *ToolAdapter, receiver *jen.Statement, ctx jen.Code, hasPayload bool, payload jen.Code, withStream bool, stream jen.Code) jen.Code {
	args := []jen.Code{ctx}
	if hasPayload {
		args = append(args, payload)
	}
	if withStream {
		args = append(args, stream)
	}
	return receiver.Dot(tool.OriginalMethodName).Call(args...)
}
