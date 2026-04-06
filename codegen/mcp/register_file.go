package codegen

import (
	"path/filepath"

	"github.com/CaliLuke/loom/codegen"
	"github.com/dave/jennifer/jen"
)

func registerFile(data *AdapterData) *codegen.File {
	if data == nil || data.Register == nil {
		return nil
	}
	svcPkg := "mcp_" + codegen.SnakeCase(data.ServiceName)
	path := filepath.Join(codegen.Gendir, svcPkg, "register.go")
	imports := []*codegen.ImportSpec{
		{Path: "context"},
		{Path: "encoding/json"},
		{Path: "errors"},
		{Path: "strings"},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/agent/planner"},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/agent/runtime", Name: "agentsruntime"},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/agent/tools"},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/mcp", Name: "mcpruntime"},
		{Path: "github.com/CaliLuke/loom-mcp/runtime/mcp/retry"},
		{Path: "github.com/modelcontextprotocol/go-sdk/jsonrpc"},
	}
	return &codegen.File{
		Path: path,
		Sections: []codegen.Section{
			codegen.Header("MCP runtime registration helpers", data.Register.Package, imports),
			codegen.MustJenniferSection("mcp-register", func(stmt *jen.Statement) {
				emitRegisterToolSpecs(stmt, data.Register)
				emitRegisterHelper(stmt, data.Register)
				emitRegisterHandleError(stmt, data.Register)
				emitRegisterRetryHint(stmt, data.Register)
			}),
		},
	}
}

func emitRegisterToolSpecs(stmt *jen.Statement, reg *RegisterData) {
	stmt.Commentf("%sToolSpecs contains the tool specifications for the %s toolset.", reg.HelperName, reg.SuiteName).Line()
	stmt.Var().Id(reg.HelperName+"ToolSpecs").Op("=").Index().Qual("github.com/CaliLuke/loom-mcp/runtime/agent/tools", "ToolSpec").ValuesFunc(func(g *jen.Group) {
		for _, tool := range reg.Tools {
			g.Add(registerToolSpecValue(reg, tool))
		}
	})
	stmt.Line()
}

func emitRegisterHelper(stmt *jen.Statement, reg *RegisterData) {
	stmt.Commentf("Register%s registers the %s toolset with the runtime.", reg.HelperName, reg.SuiteName).Line()
	stmt.Comment("The caller parameter provides the MCP client for making remote calls.").Line()
	stmt.Func().Id("Register"+reg.HelperName).
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("rt").Op("*").Id("agentsruntime").Dot("Runtime"),
			jen.Id("caller").Id("mcpruntime").Dot("Caller"),
		).
		Error().
		BlockFunc(func(g *jen.Group) {
			g.If(jen.Id("rt").Op("==").Nil()).Block(
				jen.Return(jen.Qual("errors", "New").Call(jen.Lit("runtime is required"))),
			)
			g.If(jen.Id("caller").Op("==").Nil()).Block(
				jen.Return(jen.Qual("errors", "New").Call(jen.Lit("mcp caller is required"))),
			)
			g.Line()
			g.Id("exec").Op(":=").Add(registerExecFunc(reg))
			g.Line()
			g.Return(jen.Id("rt").Dot("RegisterToolset").Call(registerToolsetRegistration(reg)))
		})
	stmt.Line()
}

func registerExecFunc(reg *RegisterData) jen.Code {
	return jen.Func().
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("call").Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "ToolRequest"),
		).
		Params(
			jen.Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "ToolResult"),
			jen.Error(),
		).
		BlockFunc(func(fn *jen.Group) {
			emitRegisterExecPrelude(fn, reg)
			emitRegisterExecPayload(fn)
			emitRegisterExecCallRemote(fn, reg)
			emitRegisterExecDecodeResult(fn)
			emitRegisterExecDecodeStructured(fn)
			fn.Return(registerToolResultValue(), jen.Nil())
		})
}

func emitRegisterExecPrelude(fn *jen.Group, reg *RegisterData) {
	fn.Id("fullName").Op(":=").Id("call").Dot("Name")
	fn.Id("toolName").Op(":=").String().Call(jen.Id("fullName"))
	fn.Const().Id("suitePrefix").Op("=").Lit(reg.SuiteQualifiedName + ".")
	fn.If(jen.Qual("strings", "HasPrefix").Call(jen.Id("toolName"), jen.Id("suitePrefix"))).Block(
		jen.Id("toolName").Op("=").Id("toolName").Index(jen.Len(jen.Id("suitePrefix")).Op(":")),
	)
}

func emitRegisterExecPayload(fn *jen.Group) {
	fn.List(jen.Id("payload"), jen.Id("err")).Op(":=").Qual("encoding/json", "Marshal").Call(jen.Id("call").Dot("Payload"))
	fn.If(jen.Id("err").Op("!=").Nil()).Block(
		jen.Return(registerErrorResultValue(), jen.Id("err")),
	)
}

func emitRegisterExecCallRemote(fn *jen.Group, reg *RegisterData) {
	fn.List(jen.Id("resp"), jen.Id("err")).Op(":=").Id("caller").Dot("CallTool").Call(
		jen.Id("ctx"),
		jen.Id("mcpruntime").Dot("CallRequest").Values(jen.Dict{
			jen.Id("Suite"):   jen.Lit(reg.SuiteQualifiedName),
			jen.Id("Tool"):    jen.Id("toolName"),
			jen.Id("Payload"): jen.Id("payload"),
		}),
	)
	fn.If(jen.Id("err").Op("!=").Nil()).Block(
		jen.Return(jen.Id(reg.HelperName+"HandleError").Call(jen.Id("fullName"), jen.Id("err")), jen.Nil()),
	)
}

func emitRegisterExecDecodeResult(fn *jen.Group) {
	fn.Var().Id("value").Any()
	fn.If(jen.Len(jen.Id("resp").Dot("Result")).Op(">").Lit(0)).Block(
		jen.If(
			jen.Id("err").Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("resp").Dot("Result"), jen.Op("&").Id("value")),
			jen.Id("err").Op("!=").Nil(),
		).Block(
			jen.Return(registerErrorResultValue(), jen.Id("err")),
		),
	)
}

func emitRegisterExecDecodeStructured(fn *jen.Group) {
	fn.Var().Id("toolTelemetry").Op("*").Qual("github.com/CaliLuke/loom-mcp/runtime/agent/telemetry", "ToolTelemetry")
	fn.If(jen.Len(jen.Id("resp").Dot("Structured")).Op(">").Lit(0)).Block(
		jen.Var().Id("structured").Any(),
		jen.If(
			jen.Id("err").Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("resp").Dot("Structured"), jen.Op("&").Id("structured")),
			jen.Id("err").Op("!=").Nil(),
		).Block(
			jen.Return(registerErrorResultValue(), jen.Id("err")),
		),
		jen.Id("toolTelemetry").Op("=").Op("&").Qual("github.com/CaliLuke/loom-mcp/runtime/agent/telemetry", "ToolTelemetry").Values(jen.Dict{
			jen.Id("Extra"): jen.Map(jen.String()).Any().Values(jen.Dict{
				jen.Lit("structured"): jen.Id("structured"),
			}),
		}),
	)
}

func registerToolsetRegistration(reg *RegisterData) jen.Code {
	return jen.Id("agentsruntime").Dot("ToolsetRegistration").Values(jen.Dict{
		jen.Id("Name"):             jen.Lit(reg.SuiteQualifiedName),
		jen.Id("Description"):      jen.Lit(reg.Description),
		jen.Id("Execute"):          registerExecuteFunc(),
		jen.Id("Specs"):            jen.Id(reg.HelperName + "ToolSpecs"),
		jen.Id("DecodeInExecutor"): jen.True(),
	})
}

func registerExecuteFunc() jen.Code {
	return jen.Func().
		Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("call").Op("*").Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "ToolRequest"),
		).
		Params(
			jen.Op("*").Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "ToolResult"),
			jen.Error(),
		).
		Block(
			jen.If(jen.Id("call").Op("==").Nil()).Block(
				jen.Return(jen.Nil(), jen.Qual("errors", "New").Call(jen.Lit("tool request is nil"))),
			),
			jen.List(jen.Id("out"), jen.Id("err")).Op(":=").Id("exec").Call(jen.Id("ctx"), jen.Op("*").Id("call")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.Return(jen.Op("&").Id("out"), jen.Nil()),
		)
}

func registerErrorResultValue() jen.Code {
	return jen.Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "ToolResult").Values(jen.Dict{
		jen.Id("Name"): jen.Id("fullName"),
	})
}

func registerToolResultValue() jen.Code {
	return jen.Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "ToolResult").Values(jen.Dict{
		jen.Id("Name"):      jen.Id("fullName"),
		jen.Id("Result"):    jen.Id("value"),
		jen.Id("Telemetry"): jen.Id("toolTelemetry"),
	})
}

func emitRegisterHandleError(stmt *jen.Statement, reg *RegisterData) {
	stmt.Commentf("%sHandleError converts an error into a tool result with appropriate retry hints.", reg.HelperName).Line()
	stmt.Func().Id(reg.HelperName+"HandleError").
		Params(
			jen.Id("toolName").Qual("github.com/CaliLuke/loom-mcp/runtime/agent/tools", "Ident"),
			jen.Id("err").Error(),
		).
		Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "ToolResult").
		Block(
			jen.Id("result").Op(":=").Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "ToolResult").Values(jen.Dict{
				jen.Id("Name"):  jen.Id("toolName"),
				jen.Id("Error"): jen.Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "ToolErrorFromError").Call(jen.Id("err")),
			}),
			jen.If(jen.Id("hint").Op(":=").Id(reg.HelperName+"RetryHint").Call(jen.Id("toolName"), jen.Id("err")), jen.Id("hint").Op("!=").Nil()).Block(
				jen.Id("result").Dot("RetryHint").Op("=").Id("hint"),
			),
			jen.Return(jen.Id("result")),
		)
	stmt.Line()
}

//nolint:maintidx // Generated retry-hint branches mirror protocol error handling in one place.
func emitRegisterRetryHint(stmt *jen.Statement, reg *RegisterData) {
	stmt.Commentf("%sRetryHint determines if an error should trigger a retry and returns appropriate hints.", reg.HelperName).Line()
	stmt.Func().Id(reg.HelperName+"RetryHint").
		Params(
			jen.Id("toolName").Qual("github.com/CaliLuke/loom-mcp/runtime/agent/tools", "Ident"),
			jen.Id("err").Error(),
		).
		Op("*").Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "RetryHint").
		BlockFunc(func(g *jen.Group) {
			g.Id("key").Op(":=").String().Call(jen.Id("toolName"))
			g.Var().Id("retryErr").Op("*").Qual("github.com/CaliLuke/loom-mcp/runtime/mcp/retry", "RetryableError")
			g.If(jen.Qual("errors", "As").Call(jen.Id("err"), jen.Op("&").Id("retryErr"))).Block(
				jen.Return(jen.Op("&").Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "RetryHint").Values(jen.Dict{
					jen.Id("Reason"):         jen.Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "RetryReasonInvalidArguments"),
					jen.Id("Tool"):           jen.Id("toolName"),
					jen.Id("Message"):        jen.Id("retryErr").Dot("Prompt"),
					jen.Id("RestrictToTool"): jen.True(),
				})),
			)
			g.Var().Id("rpcErr").Op("*").Qual("github.com/modelcontextprotocol/go-sdk/jsonrpc", "Error")
			g.If(jen.Qual("errors", "As").Call(jen.Id("err"), jen.Op("&").Id("rpcErr"))).BlockFunc(func(block *jen.Group) {
				block.Switch(jen.Id("rpcErr").Dot("Code")).Block(
					jen.Case(jen.Qual("github.com/modelcontextprotocol/go-sdk/jsonrpc", "CodeInvalidParams")).BlockFunc(func(caseBlock *jen.Group) {
						caseBlock.Var().Id("schemaJSON").String()
						caseBlock.Var().Id("example").String()
						caseBlock.Switch(jen.Id("key")).BlockFunc(func(s *jen.Group) {
							for _, tool := range reg.Tools {
								s.Case(jen.Lit(tool.ID)).Block(
									jen.Id("schemaJSON").Op("=").Lit(tool.InputSchema),
									jen.Id("example").Op("=").Lit(tool.ExampleArgs),
								)
							}
						})
						caseBlock.Id("prompt").Op(":=").Id("retry").Dot("BuildRepairPrompt").Call(
							jen.Lit("tools/call:").Op("+").Id("key"),
							jen.Id("rpcErr").Dot("Message"),
							jen.Id("example"),
							jen.Id("schemaJSON"),
						)
						caseBlock.Return(jen.Op("&").Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "RetryHint").Values(jen.Dict{
							jen.Id("Reason"):         jen.Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "RetryReasonInvalidArguments"),
							jen.Id("Tool"):           jen.Id("toolName"),
							jen.Id("Message"):        jen.Id("prompt"),
							jen.Id("RestrictToTool"): jen.True(),
						}))
					}),
					jen.Case(jen.Qual("github.com/modelcontextprotocol/go-sdk/jsonrpc", "CodeMethodNotFound")).Block(
						jen.Return(jen.Op("&").Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "RetryHint").Values(jen.Dict{
							jen.Id("Reason"):  jen.Qual("github.com/CaliLuke/loom-mcp/runtime/agent/planner", "RetryReasonToolUnavailable"),
							jen.Id("Tool"):    jen.Id("toolName"),
							jen.Id("Message"): jen.Id("rpcErr").Dot("Message"),
						})),
					),
				)
			})
			g.Return(jen.Nil())
		})
	stmt.Line()
}

func registerToolSpecValue(reg *RegisterData, tool RegisterTool) jen.Code {
	return jen.Qual("github.com/CaliLuke/loom-mcp/runtime/agent/tools", "ToolSpec").Values(jen.Dict{
		jen.Id("Name"):        jen.Lit(tool.ID),
		jen.Id("Service"):     jen.Lit(reg.ServiceName),
		jen.Id("Toolset"):     jen.Lit(reg.SuiteQualifiedName),
		jen.Id("Description"): jen.Lit(tool.Description),
		jen.Id("Meta"):        registerMetaValue(tool.Meta),
		jen.Id("Payload"): jen.Qual("github.com/CaliLuke/loom-mcp/runtime/agent/tools", "TypeSpec").Values(jen.Dict{
			jen.Id("Name"):   jen.Lit(tool.PayloadType),
			jen.Id("Schema"): jen.Index().Byte().Call(jen.Lit(tool.InputSchema)),
			jen.Id("Codec"):  jsonCodecValue(),
		}),
		jen.Id("Result"): jen.Qual("github.com/CaliLuke/loom-mcp/runtime/agent/tools", "TypeSpec").Values(jen.Dict{
			jen.Id("Name"):   jen.Lit(tool.ResultType),
			jen.Id("Schema"): jen.Nil(),
			jen.Id("Codec"):  jsonCodecValue(),
		}),
	})
}

func registerMetaValue(entries []AnnotationMetaEntry) jen.Code {
	if len(entries) == 0 {
		return jen.Nil()
	}
	dict := jen.Dict{}
	for _, entry := range entries {
		values := make([]jen.Code, 0, len(entry.Values))
		for _, value := range entry.Values {
			values = append(values, jen.Lit(value))
		}
		dict[jen.Lit(entry.Key)] = jen.Index().String().Values(values...)
	}
	return jen.Map(jen.String()).Index().String().Values(dict)
}

func jsonCodecValue() jen.Code {
	return jen.Qual("github.com/CaliLuke/loom-mcp/runtime/agent/tools", "JSONCodec").Types(jen.Any()).Values(jen.Dict{
		jen.Id("ToJSON"): jen.Func().
			Params(jen.Id("v").Any()).
			Params(jen.Index().Byte(), jen.Error()).
			Block(
				jen.Return(jen.Qual("encoding/json", "Marshal").Call(jen.Id("v"))),
			),
		jen.Id("FromJSON"): jen.Func().
			Params(jen.Id("data").Index().Byte()).
			Params(jen.Any(), jen.Error()).
			Block(
				jen.If(jen.Len(jen.Id("data")).Op("==").Lit(0)).Block(
					jen.Return(jen.Nil(), jen.Nil()),
				),
				jen.Var().Id("out").Any(),
				jen.If(
					jen.Id("err").Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Id("data"), jen.Op("&").Id("out")),
					jen.Id("err").Op("!=").Nil(),
				).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				),
				jen.Return(jen.Id("out"), jen.Nil()),
			),
	})
}
