package codegen

import (
	gocodegen "github.com/CaliLuke/loom/codegen"
	"github.com/dave/jennifer/jen"
)

func agentToolsConsumerSection(data agentToolsetConsumerFileData) gocodegen.Section {
	return gocodegen.MustJenniferSection("agent-tools-consumer", func(stmt *jen.Statement) {
		emitAgentToolsConsumerRegistration(stmt, data)
	})
}

func agentToolsSection(data agentToolsetFileData) gocodegen.Section {
	return gocodegen.MustJenniferSection("agent-tools", func(stmt *jen.Statement) {
		emitAgentToolsConstants(stmt, data)
		emitAgentToolsAliases(stmt, data)
		emitAgentToolsetRegistration(stmt, data)
		emitToolIDsVar(stmt, data)
		emitAgentNewRegistration(stmt, data)
		emitUsedToolsOptions(stmt)
		emitAgentToolCallBuilders(stmt, data)
	})
}

func mcpExecutorSection(data serviceToolsetFileData) gocodegen.Section {
	return gocodegen.MustJenniferSection("mcp-executor", func(stmt *jen.Statement) {
		emitMCPExecutorConstructor(stmt, data)
	})
}

func toolSpecsAggregateSection(data toolSpecsAggregateData) gocodegen.Section {
	return gocodegen.MustJenniferSection("tool-specs-aggregate", func(stmt *jen.Statement) {
		emitToolSpecsAggregateVars(stmt, data)
		emitToolSpecsAggregateFuncs(stmt, data)
	})
}

func usedToolsSection(data agentToolsetFileData) gocodegen.Section {
	return gocodegen.MustJenniferSection("used-tools", func(stmt *jen.Statement) {
		emitUsedToolsConstants(stmt, data)
		emitUsedToolsAliases(stmt, data)
		emitUsedToolsOptions(stmt)
		emitUsedToolsBuilders(stmt, data)
	})
}

func toolProviderSection(data toolProviderFileData) gocodegen.Section {
	return gocodegen.MustJenniferSection("tool-provider", func(stmt *jen.Statement) {
		emitToolProviderTypes(stmt, data)
		emitToolProviderHelpers(stmt)
		emitToolProviderCtor(stmt, data)
		emitToolProviderHandle(stmt, data)
		emitToolProviderBounds(stmt, data)
	})
}

func emitToolSpecsAggregateVars(stmt *jen.Statement, data toolSpecsAggregateData) {
	stmt.Var().DefsFunc(func(g *jen.Group) {
		g.Comment("Specs is the static list of tool specs exported by this agent.").Line()
		g.Id("Specs").Op("=").Index().Id("tools").Dot("ToolSpec").ValuesFunc(func(vals *jen.Group) {
			for _, ts := range data.Toolsets {
				pkg := ts.SpecsPackageName
				for _, tool := range ts.Tools {
					vals.Id(pkg).Dot("Spec" + tool.ConstName)
				}
			}
		})
		g.Line()
		g.Comment("metadata is the static list of policy metadata exported by this agent.").Line()
		g.Id("metadata").Op("=").Index().Id("policy").Dot("ToolMetadata").ValuesFunc(func(vals *jen.Group) {
			for _, ts := range data.Toolsets {
				for _, tool := range ts.Tools {
					vals.Values(jen.Dict{
						jen.Id("ID"):          jen.Id("tools").Dot("Ident").Call(jen.Lit(tool.QualifiedName)),
						jen.Id("Title"):       jen.Lit(tool.Title),
						jen.Id("Description"): jen.Lit(tool.Description),
						jen.Id("Tags"): jen.Index().String().ValuesFunc(func(tags *jen.Group) {
							for _, tag := range tool.Tags {
								tags.Lit(tag)
							}
						}),
					})
				}
			}
		})
		g.Line()
		g.Comment("names is the static list of exported tool identifiers.").Line()
		g.Id("names").Op("=").Index().Id("tools").Dot("Ident").ValuesFunc(func(vals *jen.Group) {
			for _, ts := range data.Toolsets {
				pkg := ts.SpecsPackageName
				for _, tool := range ts.Tools {
					vals.Id(pkg).Dot(tool.ConstName)
				}
			}
		})
	})
	stmt.Line()
}

func emitToolSpecsAggregateFuncs(stmt *jen.Statement, data toolSpecsAggregateData) {
	gocodegen.Doc(stmt, "Names returns the tool identifiers exported by this agent.")
	stmt.Func().Id("Names").Params().Index().Id("tools").Dot("Ident").Block(
		jen.Return(jen.Id("names")),
	)
	stmt.Line()

	gocodegen.Doc(stmt, "Spec returns the specification for the named tool if present.")
	stmt.Func().Id("Spec").Params(jen.Id("name").Id("tools").Dot("Ident")).Params(jen.Op("*").Id("tools").Dot("ToolSpec"), jen.Bool()).Block(
		jen.Switch(jen.Id("name")).BlockFunc(func(g *jen.Group) {
			for _, ts := range data.Toolsets {
				pkg := ts.SpecsPackageName
				for _, tool := range ts.Tools {
					g.Case(jen.Id("tools").Dot("Ident").Call(jen.Lit(tool.QualifiedName))).Block(
						jen.Return(jen.Op("&").Id(pkg).Dot("Spec"+tool.ConstName), jen.True()),
					)
				}
			}
			g.Default().Block(
				jen.Return(jen.Nil(), jen.False()),
			)
		}),
	)
	stmt.Line()

	gocodegen.Doc(stmt, "PayloadSchema returns the JSON schema for the named tool payload.")
	stmt.Func().Id("PayloadSchema").Params(jen.Id("name").Id("tools").Dot("Ident")).Params(jen.Index().Byte(), jen.Bool()).Block(
		jen.If(jen.List(jen.Id("s"), jen.Id("ok")).Op(":=").Id("Spec").Call(jen.Id("name")), jen.Id("ok")).Block(
			jen.Return(jen.Id("s").Dot("Payload").Dot("Schema"), jen.True()),
		),
		jen.Return(jen.Nil(), jen.False()),
	)
	stmt.Line()

	gocodegen.Doc(stmt, "ResultSchema returns the JSON schema for the named tool result.")
	stmt.Func().Id("ResultSchema").Params(jen.Id("name").Id("tools").Dot("Ident")).Params(jen.Index().Byte(), jen.Bool()).Block(
		jen.If(jen.List(jen.Id("s"), jen.Id("ok")).Op(":=").Id("Spec").Call(jen.Id("name")), jen.Id("ok")).Block(
			jen.Return(jen.Id("s").Dot("Result").Dot("Schema"), jen.True()),
		),
		jen.Return(jen.Nil(), jen.False()),
	)
	stmt.Line()

	gocodegen.Doc(stmt, "AdvertisedSpecs returns the full list of tool specs to advertise to the model.")
	stmt.Func().Id("AdvertisedSpecs").Params().Index().Id("tools").Dot("ToolSpec").Block(
		jen.Return(jen.Id("Specs")),
	)
	stmt.Line()

	gocodegen.Doc(stmt, "Metadata exposes policy metadata for the aggregated tools.")
	stmt.Func().Id("Metadata").Params().Index().Id("policy").Dot("ToolMetadata").Block(
		jen.Return(jen.Id("metadata")),
	)
	stmt.Line()
}

func emitUsedToolsConstants(stmt *jen.Statement, data agentToolsetFileData) {
	stmt.Comment("Used toolset typed helpers for " + data.Toolset.Name).Line()
	stmt.Comment("These helpers mirror the agent-as-tool helpers to provide a consistent planner UX.").Line()
	stmt.Comment("They expose typed payload/result aliases and `New<Tool>Call` builders.").Line().Line()
	stmt.Comment("Tool IDs (globally unique). Use these constants in planner tool calls.").Line()
	stmt.Const().DefsFunc(func(g *jen.Group) {
		for _, tool := range data.Tools {
			g.Id(tool.ConstName).Id("tools").Dot("Ident").Op("=").Lit(tool.Name)
		}
	})
	stmt.Line()
}

func emitAgentToolsConstants(stmt *jen.Statement, data agentToolsetFileData) {
	stmt.Comment("Name is the DSL-declared name for the exported toolset \"" + data.Toolset.Name + "\".").Line()
	stmt.Const().Id("Name").Op("=").Lit(data.Toolset.Name)
	stmt.Line()
	stmt.Comment("Service identifies the service that defined the toolset.").Line()
	stmt.Const().Id("Service").Op("=").Lit(data.Toolset.ServiceName)
	stmt.Line()
	stmt.Comment("AgentID is the fully-qualified identifier of the agent exporting this toolset.").Line()
	stmt.Const().Id("AgentID").Id("agent").Dot("Ident").Op("=").Lit(data.Toolset.Agent.ID)
	stmt.Line()
	stmt.Comment("Tool IDs for this exported toolset (globally unique). Use these typed").Line()
	stmt.Comment("constants as keys for per-tool configuration maps (e.g., SystemPrompts).").Line()
	stmt.Const().DefsFunc(func(g *jen.Group) {
		for _, tool := range data.Tools {
			g.Comment(tool.ConstName + " is the canonical tool identifier for " + tool.Name + ".").Line()
			g.Comment("Tool IDs are always the fully-qualified \"<toolset>.<tool>\" form so they").Line()
			g.Comment("match Specs entries, planner requests, and runtime stream events exactly.").Line()
			g.Id(tool.ConstName).Id("tools").Dot("Ident").Op("=").Lit(tool.Name)
		}
	})
	stmt.Line()
}

func emitAgentToolsAliases(stmt *jen.Statement, data agentToolsetFileData) {
	stmt.Comment("Type aliases and codec re-exports for convenience. These aliases preserve exact").Line()
	stmt.Comment("type identity while allowing callers to avoid importing the specs package.").Line()
	for _, tool := range data.Tools {
		stmt.Type().Id(tool.GoName + "Payload").Op("=").Id(data.Toolset.SpecsPackageName + "specs").Dot(tool.Payload.TypeName)
		stmt.Line()
		stmt.Type().Id(tool.GoName + "Result").Op("=").Id(data.Toolset.SpecsPackageName + "specs").Dot(tool.Result.TypeName)
		stmt.Line()
		stmt.Var().Id(tool.GoName + "PayloadCodec").Op("=").Id(data.Toolset.SpecsPackageName + "specs").Dot(tool.Payload.ExportedCodec)
		stmt.Line()
		stmt.Var().Id(tool.GoName + "ResultCodec").Op("=").Id(data.Toolset.SpecsPackageName + "specs").Dot(tool.Result.ExportedCodec)
		stmt.Line()
	}
	stmt.Line()
}

func emitUsedToolsAliases(stmt *jen.Statement, data agentToolsetFileData) {
	stmt.Comment("Type aliases and codec re-exports for convenience.").Line()
	for _, tool := range data.Tools {
		stmt.Type().Id(tool.GoName + "Payload").Op("=").Id(data.Toolset.SpecsPackageName + "specs").Dot(tool.Payload.TypeName)
		stmt.Line()
		stmt.Var().Id(tool.GoName + "PayloadCodec").Op("=").Id(data.Toolset.SpecsPackageName + "specs").Dot(tool.Payload.ExportedCodec)
		stmt.Line()
		if tool.Result != nil {
			stmt.Type().Id(tool.GoName + "Result").Op("=").Id(data.Toolset.SpecsPackageName + "specs").Dot(tool.Result.TypeName)
			stmt.Line()
			stmt.Var().Id(tool.GoName + "ResultCodec").Op("=").Id(data.Toolset.SpecsPackageName + "specs").Dot(tool.Result.ExportedCodec)
			stmt.Line()
		}
	}
	stmt.Line()
}

func emitUsedToolsOptions(stmt *jen.Statement) {
	gocodegen.Doc(stmt, "CallOption customizes planner.ToolRequest values built by the typed helpers.")
	stmt.Type().Id("CallOption").Func().Params(jen.Op("*").Id("planner").Dot("ToolRequest"))
	stmt.Line()
	gocodegen.Doc(stmt, "WithParentToolCallID sets the ParentToolCallID on the constructed request.")
	stmt.Func().Id("WithParentToolCallID").Params(jen.Id("id").String()).Id("CallOption").Block(
		jen.Return(jen.Func().Params(jen.Id("r").Op("*").Id("planner").Dot("ToolRequest")).Block(
			jen.Id("r").Dot("ParentToolCallID").Op("=").Id("id"),
		)),
	)
	stmt.Line()
	gocodegen.Doc(stmt, "WithToolCallID sets a model/tool-call identifier on the request. The runtime\npreserves this ID and echoes it in ToolResult.ToolCallID for correlation.")
	stmt.Func().Id("WithToolCallID").Params(jen.Id("id").String()).Id("CallOption").Block(
		jen.Return(jen.Func().Params(jen.Id("r").Op("*").Id("planner").Dot("ToolRequest")).Block(
			jen.Id("r").Dot("ToolCallID").Op("=").Id("id"),
		)),
	)
	stmt.Line()
}

func emitUsedToolsBuilders(stmt *jen.Statement, data agentToolsetFileData) {
	stmt.Comment("Typed tool-call helpers (one per tool). These ensure use of the generated tool ID").Line()
	stmt.Comment("and accept typed payloads matching tool schemas.").Line()
	for _, tool := range data.Tools {
		gocodegen.Doc(stmt, "New"+tool.GoName+"Call builds a planner.ToolRequest for "+tool.Name+".")
		stmt.Func().Id("New"+tool.GoName+"Call").
			Params(jen.Id("args").Op("*").Id(tool.GoName+"Payload"), jen.Id("opts").Op("...").Id("CallOption")).
			Id("planner").Dot("ToolRequest").
			Block(
				jen.Var().Id("payload").Index().Byte(),
				jen.If(jen.Id("args").Op("!=").Nil()).Block(
					jen.Comment("Encode typed payloads into canonical JSON using the generated codec."),
					jen.List(jen.Id("b"), jen.Id("err")).Op(":=").Id(tool.GoName+"PayloadCodec").Dot("ToJSON").Call(jen.Id("args")),
					jen.If(jen.Id("err").Op("!=").Nil()).Block(
						jen.Panic(jen.Id("err")),
					),
					jen.Id("payload").Op("=").Id("b"),
				),
				jen.Id("req").Op(":=").Id("planner").Dot("ToolRequest").Values(jen.Dict{
					jen.Id("Name"):    jen.Id(tool.ConstName),
					jen.Id("Payload"): jen.Id("payload"),
				}),
				jen.For(jen.List(jen.Id("_"), jen.Id("o")).Op(":=").Range().Id("opts")).Block(
					jen.If(jen.Id("o").Op("!=").Nil()).Block(
						jen.Id("o").Call(jen.Op("&").Id("req")),
					),
				),
				jen.Return(jen.Id("req")),
			)
		stmt.Line()
	}
}

func emitAgentToolsetRegistration(stmt *jen.Statement, data agentToolsetFileData) {
	funcName := "New" + data.Toolset.Agent.GoName + "ToolsetRegistration"
	doc := funcName + " creates a toolset registration for the " + data.Toolset.Agent.Name + " agent.\n" +
		"The returned registration can be used with runtime.RegisterToolset to make the agent\n" +
		"available as a tool to other agents. When invoked, the agent runs its full planning loop\n" +
		"and returns the final response as the tool result. DSL-authored CallHintTemplate and\n" +
		"ResultHintTemplate declarations are compiled into hint templates so sinks can render\n" +
		"concise labels and previews without heuristics.\n\nExample usage:\n\n" +
		"\trt := runtime.New(...)\n" +
		"\treg := " + funcName + "(rt)\n" +
		"\tif err := rt.RegisterToolset(reg); err != nil {\n" +
		"\t\t// handle error\n" +
		"\t}"
	gocodegen.Doc(stmt, doc)
	stmt.Func().Id(funcName).
		Params(jen.Id("rt").Op("*").Id("runtime").Dot("Runtime")).
		Id("runtime").Dot("ToolsetRegistration").
		Block(
			jen.Id("cfg").Op(":=").Id("runtime").Dot("AgentToolConfig").Values(jen.Dict{
				jen.Id("AgentID"):             jen.Id("AgentID"),
				jen.Id("Name"):                jen.Lit(data.Toolset.QualifiedName),
				jen.Id("TaskQueue"):           jen.Lit(data.Toolset.TaskQueue),
				jen.Id("Route"):               agentRouteLiteral(data),
				jen.Id("PlanActivityName"):    jen.Lit(data.Toolset.Agent.Runtime.PlanActivity.Name),
				jen.Id("ResumeActivityName"):  jen.Lit(data.Toolset.Agent.Runtime.ResumeActivity.Name),
				jen.Id("ExecuteToolActivity"): jen.Lit(data.Toolset.Agent.Runtime.ExecuteTool.Name),
			}),
			jen.Id("reg").Op(":=").Id("runtime").Dot("NewAgentToolsetRegistration").Call(jen.Id("rt"), jen.Id("cfg")),
			jen.Id("reg").Dot("Specs").Op("=").Id(data.Toolset.SpecsPackageName+"specs").Dot("Specs"),
			emitHintTemplateAssignments(data, false),
			jen.Return(jen.Id("reg")),
		)
	stmt.Line()
}

func emitToolIDsVar(stmt *jen.Statement, data agentToolsetFileData) {
	stmt.Comment("ToolIDs lists all tools in this toolset for validation.").Line()
	stmt.Var().Id("ToolIDs").Op("=").Index().Id("tools").Dot("Ident").ValuesFunc(func(g *jen.Group) {
		for _, tool := range data.Toolset.Tools {
			g.Id(tool.ConstName)
		}
	})
	stmt.Line()
}

func emitAgentNewRegistration(stmt *jen.Statement, data agentToolsetFileData) {
	gocodegen.Doc(stmt, "NewRegistration creates a toolset registration with an optional agent-wide\nsystem prompt and per-tool content configured via runtime options. Callers\ncan mix text and templates; each tool must be configured in exactly one way.")
	stmt.Func().Id("NewRegistration").
		Params(
			jen.Id("rt").Op("*").Id("runtime").Dot("Runtime"),
			jen.Id("systemPrompt").String(),
			jen.Id("opts").Op("...").Id("runtime").Dot("AgentToolOption"),
		).
		Params(jen.Id("runtime").Dot("ToolsetRegistration"), jen.Error()).
		Block(
			jen.Id("cfg").Op(":=").Id("runtime").Dot("AgentToolConfig").Values(jen.Dict{
				jen.Id("AgentID"):             jen.Id("AgentID"),
				jen.Id("Name"):                jen.Lit(data.Toolset.QualifiedName),
				jen.Id("TaskQueue"):           jen.Lit(data.Toolset.TaskQueue),
				jen.Id("SystemPrompt"):        jen.Id("systemPrompt"),
				jen.Id("Route"):               agentRouteLiteral(data),
				jen.Id("PlanActivityName"):    jen.Lit(data.Toolset.Agent.Runtime.PlanActivity.Name),
				jen.Id("ResumeActivityName"):  jen.Lit(data.Toolset.Agent.Runtime.ResumeActivity.Name),
				jen.Id("ExecuteToolActivity"): jen.Lit(data.Toolset.Agent.Runtime.ExecuteTool.Name),
			}),
			jen.For(jen.List(jen.Id("_"), jen.Id("o")).Op(":=").Range().Id("opts")).Block(
				jen.Id("o").Call(jen.Op("&").Id("cfg")),
			),
			jen.If(jen.Len(jen.Id("cfg").Dot("Templates")).Op(">").Lit(0)).Block(
				jen.Id("ids").Op(":=").Make(jen.Index().Id("tools").Dot("Ident"), jen.Lit(0), jen.Len(jen.Id("cfg").Dot("Templates"))),
				jen.For(jen.Id("id").Op(":=").Range().Id("cfg").Dot("Templates")).Block(
					jen.Id("ids").Op("=").Append(jen.Id("ids"), jen.Id("id")),
				),
				jen.If(jen.Id("err").Op(":=").Id("runtime").Dot("ValidateAgentToolTemplates").Call(
					jen.Id("cfg").Dot("Templates"),
					jen.Id("ids"),
					jen.Nil(),
				), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Id("runtime").Dot("ToolsetRegistration").Values(), jen.Id("err")),
				),
			),
			jen.Id("reg").Op(":=").Id("runtime").Dot("NewAgentToolsetRegistration").Call(jen.Id("rt"), jen.Id("cfg")),
			jen.Id("reg").Dot("Specs").Op("=").Id(data.Toolset.SpecsPackageName+"specs").Dot("Specs"),
			emitHintTemplateAssignments(data, true),
			jen.Return(jen.Id("reg"), jen.Nil()),
		)
	stmt.Line()
}

func emitAgentToolCallBuilders(stmt *jen.Statement, data agentToolsetFileData) {
	stmt.Comment("Typed tool-call helpers for each tool in this exported toolset. These helpers").Line()
	stmt.Comment("enforce use of the generated tool identifier and accept a typed payload that").Line()
	stmt.Comment("matches the tool schema.").Line()
	for _, tool := range data.Toolset.Tools {
		gocodegen.Doc(stmt, "New"+gocodegen.Goify(tool.Name, true)+"Call builds a planner.ToolRequest for the "+tool.QualifiedName+" tool.")
		stmt.Func().Id("New"+gocodegen.Goify(tool.Name, true)+"Call").
			Params(jen.Id("args").Op("*").Id(gocodegen.Goify(tool.Name, true)+"Payload"), jen.Id("opts").Op("...").Id("CallOption")).
			Id("planner").Dot("ToolRequest").
			Block(
				jen.Var().Id("payload").Index().Byte(),
				jen.If(jen.Id("args").Op("!=").Nil()).Block(
					jen.List(jen.Id("b"), jen.Id("err")).Op(":=").Id(gocodegen.Goify(tool.Name, true)+"PayloadCodec").Dot("ToJSON").Call(jen.Id("args")),
					jen.If(jen.Id("err").Op("!=").Nil()).Block(
						jen.Panic(jen.Id("err")),
					),
					jen.Id("payload").Op("=").Id("b"),
				),
				jen.Id("req").Op(":=").Id("planner").Dot("ToolRequest").Values(jen.Dict{
					jen.Id("Name"):    jen.Id(tool.ConstName),
					jen.Id("Payload"): jen.Id("payload"),
				}),
				jen.For(jen.List(jen.Id("_"), jen.Id("o")).Op(":=").Range().Id("opts")).Block(
					jen.If(jen.Id("o").Op("!=").Nil()).Block(
						jen.Id("o").Call(jen.Op("&").Id("req")),
					),
				),
				jen.Return(jen.Id("req")),
			)
		stmt.Line()
	}
}

func agentRouteLiteral(data agentToolsetFileData) *jen.Statement {
	return jen.Id("runtime").Dot("AgentRoute").Values(jen.Dict{
		jen.Id("ID"):               jen.Id("AgentID"),
		jen.Id("WorkflowName"):     jen.Lit(data.Toolset.Agent.Runtime.Workflow.Name),
		jen.Id("DefaultTaskQueue"): jen.Lit(data.Toolset.Agent.Runtime.Workflow.Queue),
	})
}

func emitHintTemplateAssignments(data agentToolsetFileData, returnsError bool) *jen.Statement {
	hasCallHints := false
	hasResultHints := false
	for _, tool := range data.Toolset.Tools {
		if tool.CallHintTemplate != "" {
			hasCallHints = true
		}
		if tool.ResultHintTemplate != "" {
			hasResultHints = true
		}
	}
	if !hasCallHints && !hasResultHints {
		return jen.Empty()
	}

	return jen.BlockFunc(func(g *jen.Group) {
		if hasCallHints {
			g.BlockFunc(func(cg *jen.Group) {
				emitCompiledHints(cg, data, returnsError, "CallHints", func(tool *ToolData) string { return tool.CallHintTemplate })
			})
		}
		if hasResultHints {
			g.BlockFunc(func(cg *jen.Group) {
				emitCompiledHints(cg, data, returnsError, "ResultHints", func(tool *ToolData) string { return tool.ResultHintTemplate })
			})
		}
	})
}

func emitCompiledHints(g *jen.Group, data agentToolsetFileData, returnsError bool, targetField string, templateFor func(*ToolData) string) {
	g.List(jen.Id("compiled"), jen.Id("err")).Op(":=").Id("hints").Dot("CompileHintTemplates").Call(
		jen.Map(jen.Id("tools").Dot("Ident")).String().ValuesFunc(func(vals *jen.Group) {
			for _, tool := range data.Toolset.Tools {
				tpl := templateFor(tool)
				if tpl == "" {
					continue
				}
				vals.Id(tool.ConstName).Op(":").Lit(tpl)
			}
		}),
		jen.Nil(),
	)
	g.If(jen.Id("err").Op("!=").Nil()).BlockFunc(func(ig *jen.Group) {
		if returnsError {
			ig.Return(jen.Id("runtime").Dot("ToolsetRegistration").Values(), jen.Id("err"))
		} else {
			ig.Panic(jen.Id("err"))
		}
	})
	g.Id("reg").Dot(targetField).Op("=").Id("compiled")
}

func emitToolProviderTypes(stmt *jen.Statement, data toolProviderFileData) {
	stmt.Type().Defs(
		jen.Comment("Provider dispatches tool call messages to the bound Goa service methods and").Line().
			Comment("returns canonical JSON tool results and typed server-only data.").Line().
			Line().
			Comment("Provider is intended to run inside the toolset-owning service process,").Line().
			Comment("paired with a Pulse subscription loop (see runtime/toolregistry/provider).").Line().
			Id("Provider").Struct(
			jen.Id("svc").Add(gocodegen.TypeRef(data.ServiceTypeRef)),
		),
	)
	stmt.Line()
}

func emitToolProviderHelpers(stmt *jen.Statement) {
	stmt.Func().Id("toolErrorCode").Params(jen.Id("err").Error()).String().Block(
		jen.Var().Id("se").Op("*").Id("goa").Dot("ServiceError"),
		jen.If(jen.Qual("errors", "As").Call(jen.Id("err"), jen.Op("&").Id("se"))).Block(
			jen.If(jen.Id("se").Dot("Timeout")).Block(
				jen.Return(jen.Lit("timeout")),
			),
			jen.If(jen.Id("se").Dot("Name").Op("!=").Lit("")).Block(
				jen.Return(jen.Id("se").Dot("Name")),
			),
		),
		jen.Return(jen.Lit("execution_failed")),
	)
	stmt.Line()
}

func emitToolProviderCtor(stmt *jen.Statement, data toolProviderFileData) {
	gocodegen.Doc(stmt, "NewProvider returns a Provider for the toolset.")
	stmt.Func().Id("NewProvider").Params(jen.Id("svc").Add(gocodegen.TypeRef(data.ServiceTypeRef))).Op("*").Id("Provider").Block(
		jen.If(jen.Id("svc").Op("==").Nil()).Block(
			jen.Panic(jen.Lit("tool provider service is required")),
		),
		jen.Return(jen.Op("&").Id("Provider").Values(jen.Dict{
			jen.Id("svc"): jen.Id("svc"),
		})),
	)
	stmt.Line()
}

func emitToolProviderHandle(stmt *jen.Statement, data toolProviderFileData) {
	gocodegen.Doc(stmt, "HandleToolCall executes the requested tool and returns a tool result message.")
	stmt.Func().Params(jen.Id("p").Op("*").Id("Provider")).Id("HandleToolCall").
		Params(jen.Id("ctx").Qual("context", "Context"), jen.Id("msg").Id("toolregistry").Dot("ToolCallMessage")).
		Params(jen.Id("toolregistry").Dot("ToolResultMessage"), jen.Error()).
		Block(
			jen.If(jen.Id("msg").Dot("ToolUseID").Op("==").Lit("")).Block(
				jen.Return(jen.Id("toolregistry").Dot("NewToolResultErrorMessage").Call(jen.Lit(""), jen.Lit("invalid_call"), jen.Lit("tool_use_id is required")), jen.Nil()),
			),
			jen.If(jen.Id("msg").Dot("Meta").Op("==").Nil()).Block(
				jen.Return(jen.Id("toolregistry").Dot("NewToolResultErrorMessage").Call(jen.Id("msg").Dot("ToolUseID"), jen.Lit("invalid_call"), jen.Lit("meta is required")), jen.Nil()),
			),
			jen.Switch(jen.Id("msg").Dot("Tool")).BlockFunc(func(g *jen.Group) {
				for _, tool := range data.Tools {
					if !tool.IsMethodBacked {
						continue
					}
					g.Case(jen.Id(tool.ConstName)).BlockFunc(func(cg *jen.Group) {
						cg.List(jen.Id("args"), jen.Id("err")).Op(":=").Id(tool.ConstName + "PayloadCodec").Dot("FromJSON").Call(jen.Id("msg").Dot("Payload"))
						cg.If(jen.Id("err").Op("!=").Nil()).Block(
							jen.If(jen.List(jen.Id("issues")).Op(":=").Id("toolregistry").Dot("ValidationIssues").Call(jen.Id("err")), jen.Len(jen.Id("issues")).Op(">").Lit(0)).Block(
								jen.Return(jen.Id("toolregistry").Dot("NewToolResultErrorMessageWithIssues").Call(jen.Id("msg").Dot("ToolUseID"), jen.Lit("invalid_arguments"), jen.Id("err").Dot("Error").Call(), jen.Id("issues")), jen.Nil()),
							),
							jen.Return(jen.Id("toolregistry").Dot("NewToolResultErrorMessage").Call(jen.Id("msg").Dot("ToolUseID"), jen.Lit("invalid_arguments"), jen.Id("err").Dot("Error").Call()), jen.Nil()),
						)
						cg.Id("methodIn").Op(":=").Id("Init" + tool.ConstName + "MethodPayload").Call(jen.Id("args"))
						for _, field := range tool.InjectedFields {
							cg.Id("methodIn").Dot(gocodegen.Goify(field, true)).Op("=").Id("msg").Dot("Meta").Dot(gocodegen.Goify(field, true))
						}
						cg.List(jen.Id("methodOut"), jen.Id("err")).Op(":=").Id("p").Dot("svc").Dot(tool.MethodGoName).Call(jen.Id("ctx"), jen.Id("methodIn"))
						cg.If(jen.Id("err").Op("!=").Nil()).Block(
							jen.If(jen.List(jen.Id("issues")).Op(":=").Id("toolregistry").Dot("ValidationIssues").Call(jen.Id("err")), jen.Len(jen.Id("issues")).Op(">").Lit(0)).Block(
								jen.Return(jen.Id("toolregistry").Dot("NewToolResultErrorMessageWithIssues").Call(jen.Id("msg").Dot("ToolUseID"), jen.Lit("invalid_arguments"), jen.Id("err").Dot("Error").Call(), jen.Id("issues")), jen.Nil()),
							),
							jen.Return(jen.Id("toolregistry").Dot("NewToolResultErrorMessage").Call(jen.Id("msg").Dot("ToolUseID"), jen.Id("toolErrorCode").Call(jen.Id("err")), jen.Id("err").Dot("Error").Call()), jen.Nil()),
						)
						if tool.HasResult {
							cg.Id("result").Op(":=").Id("Init" + tool.ConstName + "ToolResult").Call(jen.Id("methodOut"))
							cg.List(jen.Id("resultJSON"), jen.Id("err")).Op(":=").Id(tool.ConstName + "ResultCodec").Dot("ToJSON").Call(jen.Id("result"))
							cg.If(jen.Id("err").Op("!=").Nil()).Block(
								jen.Return(jen.Id("toolregistry").Dot("NewToolResultErrorMessage").Call(jen.Id("msg").Dot("ToolUseID"), jen.Lit("encode_failed"), jen.Id("err").Dot("Error").Call()), jen.Nil()),
							)
							if tool.Bounds != nil && tool.Bounds.Projection != nil && tool.Bounds.Projection.Returned != nil && tool.Bounds.Projection.Truncated != nil {
								cg.Id("bounds").Op(":=").Id("init" + gocodegen.Goify(tool.Name, true) + "Bounds").Call(jen.Id("methodOut"))
							}
							cg.Var().Id("server").Index().Op("*").Id("toolregistry").Dot("ServerDataItem")
							for _, sd := range tool.ServerData {
								if sd.MethodResultField == "" {
									continue
								}
								typeName := "Init" + tool.ConstName + gocodegen.Goify(sd.Kind, true) + "ServerData"
								codecName := tool.ConstName + gocodegen.Goify(sd.Kind, true) + "ServerDataCodec"
								cg.Block(
									jen.Id("data").Op(":=").Id(typeName).Call(jen.Id("methodOut").Dot(gocodegen.Goify(sd.MethodResultField, true))),
									jen.List(jen.Id("dataJSON"), jen.Id("err")).Op(":=").Id(codecName).Dot("ToJSON").Call(jen.Id("data")),
									jen.If(jen.Id("err").Op("!=").Nil()).Block(
										jen.Return(jen.Id("toolregistry").Dot("NewToolResultErrorMessage").Call(jen.Id("msg").Dot("ToolUseID"), jen.Lit("encode_failed"), jen.Id("err").Dot("Error").Call()), jen.Nil()),
									),
									jen.If(jen.String().Call(jen.Id("dataJSON")).Op("!=").Lit("null")).Block(
										jen.Id("server").Op("=").Append(jen.Id("server"), jen.Op("&").Id("toolregistry").Dot("ServerDataItem").Values(jen.Dict{
											jen.Id("Kind"):     jen.Lit(sd.Kind),
											jen.Id("Audience"): jen.Lit(sd.Audience),
											jen.Id("Data"):     jen.Id("dataJSON"),
										})),
									),
								)
							}
							cg.If(jen.Len(jen.Id("server")).Op(">").Lit(0)).BlockFunc(func(rg *jen.Group) {
								dict := jen.Dict{
									jen.Id("ToolUseID"):  jen.Id("msg").Dot("ToolUseID"),
									jen.Id("Result"):     jen.Id("resultJSON"),
									jen.Id("ServerData"): jen.Id("server"),
								}
								if tool.Bounds != nil && tool.Bounds.Projection != nil && tool.Bounds.Projection.Returned != nil && tool.Bounds.Projection.Truncated != nil {
									dict[jen.Id("Bounds")] = jen.Id("bounds")
								}
								rg.Return(jen.Id("toolregistry").Dot("ToolResultMessage").Values(dict), jen.Nil())
							})
							dict := jen.Dict{
								jen.Id("ToolUseID"): jen.Id("msg").Dot("ToolUseID"),
								jen.Id("Result"):    jen.Id("resultJSON"),
							}
							if tool.Bounds != nil && tool.Bounds.Projection != nil && tool.Bounds.Projection.Returned != nil && tool.Bounds.Projection.Truncated != nil {
								dict[jen.Id("Bounds")] = jen.Id("bounds")
							}
							cg.Return(jen.Id("toolregistry").Dot("ToolResultMessage").Values(dict), jen.Nil())
						} else {
							cg.Return(jen.Id("toolregistry").Dot("NewToolResultMessage").Call(jen.Id("msg").Dot("ToolUseID"), jen.Nil()), jen.Nil())
						}
					})
				}
				g.Default().Block(
					jen.Return(jen.Id("toolregistry").Dot("NewToolResultErrorMessage").Call(jen.Id("msg").Dot("ToolUseID"), jen.Lit("unknown_tool"), jen.Qual("fmt", "Sprintf").Call(jen.Lit("unknown tool %q"), jen.Id("msg").Dot("Tool"))), jen.Nil()),
				)
			}),
		)
	stmt.Line()
}

func emitAgentToolsConsumerRegistration(stmt *jen.Statement, data agentToolsetConsumerFileData) {
	funcName := "New" + data.Agent.GoName + gocodegen.Goify(data.Toolset.PathName, true) + "AgentToolsetRegistration"
	doc := funcName + " creates a ToolsetRegistration for the " + data.Toolset.Name + " toolset exported by the " +
		data.Toolset.SourceServiceName + " service. It delegates to the provider's agenttools.NewRegistration helper " +
		"so callers can configure system prompts and AgentToolOption values while keeping routing metadata centralized " +
		"with the exporting agent.\n\nExample:\n\n" +
		"\treg, err := " + funcName + "(\n" +
		"\t    rt,\n" +
		"\t    systemPrompt,\n" +
		"\t    opts...,\n" +
		"\t)\n" +
		"\tif err != nil {\n" +
		"\t    return err\n" +
		"\t}\n" +
		"\tif err := rt.RegisterToolset(reg); err != nil {\n" +
		"\t    return err\n" +
		"\t}"
	gocodegen.Doc(stmt, doc)
	stmt.Func().Id(funcName).
		Params(
			jen.Id("rt").Op("*").Id("runtime").Dot("Runtime"),
			jen.Id("systemPrompt").String(),
			jen.Id("opts").Op("...").Id("runtime").Dot("AgentToolOption"),
		).
		Params(jen.Id("runtime").Dot("ToolsetRegistration"), jen.Error()).
		Block(
			jen.Return(jen.Id(data.ProviderAlias).Dot("NewRegistration").Call(
				jen.Id("rt"),
				jen.Id("systemPrompt"),
				jen.Id("opts").Op("..."),
			)),
		)
	stmt.Line()
}

func emitMCPExecutorConstructor(stmt *jen.Statement, data serviceToolsetFileData) {
	funcName := "New" + data.Agent.GoName + gocodegen.Goify(data.Toolset.PathName, true) + "MCPExecutor"
	gocodegen.Doc(stmt, funcName+" returns a ToolCallExecutor that proxies tool calls to an MCP caller using generated per-toolset codecs.")
	stmt.Func().Id(funcName).
		Params(jen.Id("caller").Id("mcpruntime").Dot("Caller")).
		Id("runtime").Dot("ToolCallExecutor").
		Block(
			jen.Id("suite").Op(":=").Lit(data.Toolset.QualifiedName),
			jen.Id("prefix").Op(":=").Id("suite").Op("+").Lit("."),
			jen.Return(
				jen.Id("runtime").Dot("ToolCallExecutorFunc").Call(
					jen.Func().
						Params(
							jen.Id("ctx").Qual("context", "Context"),
							jen.Id("meta").Op("*").Id("runtime").Dot("ToolCallMeta"),
							jen.Id("call").Op("*").Id("planner").Dot("ToolRequest"),
						).
						Params(jen.Op("*").Id("planner").Dot("ToolResult"), jen.Error()).
						Block(
							jen.If(jen.Id("call").Op("==").Nil()).Block(
								jen.Return(
									jen.Op("&").Id("planner").Dot("ToolResult").Values(jen.Dict{
										jen.Id("Error"): jen.Id("planner").Dot("NewToolError").Call(jen.Lit("tool request is nil")),
									}),
									jen.Nil(),
								),
							),
							jen.If(jen.Id("meta").Op("==").Nil()).Block(
								jen.Return(
									jen.Op("&").Id("planner").Dot("ToolResult").Values(jen.Dict{
										jen.Id("Error"): jen.Id("planner").Dot("NewToolError").Call(jen.Lit("tool call meta is nil")),
									}),
									jen.Nil(),
								),
							),
							jen.Id("full").Op(":=").Id("call").Dot("Name"),
							jen.Id("tool").Op(":=").Id("full"),
							jen.If(jen.Qual("strings", "HasPrefix").Call(jen.Id("tool"), jen.Id("prefix"))).Block(
								jen.Id("tool").Op("=").Id("tool").Index(jen.Len(jen.Id("prefix")).Op(":")),
							),
							jen.If(
								jen.List(jen.Id("pc"), jen.Id("ok")).Op(":=").Id(data.Toolset.SpecsPackageName).Dot("PayloadCodec").Call(jen.Id("full")),
								jen.Id("ok"),
							).Block(
								jen.List(jen.Id("payload"), jen.Id("err")).Op(":=").Id("pc").Dot("ToJSON").Call(jen.Id("call").Dot("Payload")),
								jen.If(jen.Id("err").Op("!=").Nil()).Block(
									jen.Return(
										jen.Op("&").Id("planner").Dot("ToolResult").Values(jen.Dict{
											jen.Id("Name"):  jen.Id("full"),
											jen.Id("Error"): jen.Id("planner").Dot("ToolErrorFromError").Call(jen.Id("err")),
										}),
										jen.Nil(),
									),
								),
								jen.List(jen.Id("resp"), jen.Id("err")).Op(":=").Id("caller").Dot("CallTool").Call(
									jen.Id("ctx"),
									jen.Id("mcpruntime").Dot("CallRequest").Values(jen.Dict{
										jen.Id("Suite"):   jen.Id("suite"),
										jen.Id("Tool"):    jen.Id("tool"),
										jen.Id("Payload"): jen.Id("payload"),
									}),
								),
								jen.If(jen.Id("err").Op("!=").Nil()).Block(
									jen.Return(
										jen.Op("&").Id("planner").Dot("ToolResult").Values(jen.Dict{
											jen.Id("Name"):  jen.Id("full"),
											jen.Id("Error"): jen.Id("planner").Dot("ToolErrorFromError").Call(jen.Id("err")),
										}),
										jen.Nil(),
									),
								),
								jen.Var().Id("value").Any(),
								jen.If(jen.Len(jen.Id("resp").Dot("Result")).Op(">").Lit(0)).Block(
									jen.If(
										jen.List(jen.Id("rc"), jen.Id("ok")).Op(":=").Id(data.Toolset.SpecsPackageName).Dot("ResultCodec").Call(jen.Id("full")),
										jen.Id("ok"),
									).Block(
										jen.List(jen.Id("v"), jen.Id("err")).Op(":=").Id("rc").Dot("FromJSON").Call(jen.Id("resp").Dot("Result")),
										jen.If(jen.Id("err").Op("!=").Nil()).Block(
											jen.Return(
												jen.Op("&").Id("planner").Dot("ToolResult").Values(jen.Dict{
													jen.Id("Name"):  jen.Id("full"),
													jen.Id("Error"): jen.Id("planner").Dot("ToolErrorFromError").Call(jen.Id("err")),
												}),
												jen.Nil(),
											),
										),
										jen.Id("value").Op("=").Id("v"),
									),
								),
								jen.Var().Id("tel").Op("*").Id("telemetry").Dot("ToolTelemetry"),
								jen.If(jen.Len(jen.Id("resp").Dot("Structured")).Op(">").Lit(0)).Block(
									jen.Id("tel").Op("=").Op("&").Id("telemetry").Dot("ToolTelemetry").Values(jen.Dict{
										jen.Id("Extra"): jen.Map(jen.String()).Any().Values(jen.Dict{
											jen.Lit("structured"): jen.Qual("encoding/json", "RawMessage").Call(jen.Id("resp").Dot("Structured")),
										}),
									}),
								),
								jen.Return(
									jen.Op("&").Id("planner").Dot("ToolResult").Values(jen.Dict{
										jen.Id("Name"):      jen.Id("full"),
										jen.Id("Result"):    jen.Id("value"),
										jen.Id("Telemetry"): jen.Id("tel"),
									}),
									jen.Nil(),
								),
							),
							jen.Return(
								jen.Op("&").Id("planner").Dot("ToolResult").Values(jen.Dict{
									jen.Id("Name"):  jen.Id("full"),
									jen.Id("Error"): jen.Id("planner").Dot("NewToolError").Call(jen.Lit("payload codec not found")),
								}),
								jen.Nil(),
							),
						),
				),
			),
		)
	stmt.Line()
}

func emitToolProviderBounds(stmt *jen.Statement, data toolProviderFileData) {
	for _, tool := range data.Tools {
		if !tool.IsMethodBacked || tool.Bounds == nil || tool.Bounds.Projection == nil || tool.Bounds.Projection.Returned == nil || tool.Bounds.Projection.Truncated == nil {
			continue
		}
		name := "init" + gocodegen.Goify(tool.Name, true) + "Bounds"
		gocodegen.Doc(stmt, name+" projects canonical bounds metadata from the\nbound method result.")
		stmt.Func().Id(name).Params(jen.Id("mr").Add(gocodegen.TypeRef(tool.MethodResultTypeRef))).Op("*").Id("agent").Dot("Bounds").BlockFunc(func(g *jen.Group) {
			g.Id("bounds").Op(":=").Op("&").Id("agent").Dot("Bounds").Values()
			if f := tool.Bounds.Projection.Returned; f != nil {
				g.Id("bounds").Dot("Returned").Op("=").Id("mr").Dot(f.Name)
			}
			if f := tool.Bounds.Projection.Total; f != nil {
				if f.Required {
					g.Id("total").Op(":=").Id("mr").Dot(f.Name)
					g.Id("bounds").Dot("Total").Op("=").Op("&").Id("total")
				} else {
					g.Id("bounds").Dot("Total").Op("=").Id("mr").Dot(f.Name)
				}
			}
			if f := tool.Bounds.Projection.Truncated; f != nil {
				g.Id("bounds").Dot("Truncated").Op("=").Id("mr").Dot(f.Name)
			}
			if f := tool.Bounds.Projection.NextCursor; f != nil {
				g.Id("bounds").Dot("NextCursor").Op("=").Id("mr").Dot(f.Name)
			}
			if f := tool.Bounds.Projection.RefinementHint; f != nil {
				if f.Required {
					g.Id("bounds").Dot("RefinementHint").Op("=").Id("mr").Dot(f.Name)
				} else {
					g.If(jen.Id("mr").Dot(f.Name).Op("!=").Nil()).Block(
						jen.Id("bounds").Dot("RefinementHint").Op("=").Op("*").Id("mr").Dot(f.Name),
					)
				}
			}
			g.Return(jen.Id("bounds"))
		})
		stmt.Line()
	}
}
