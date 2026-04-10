package codegen

import (
	"github.com/CaliLuke/loom/codegen"
	"github.com/dave/jennifer/jen"
)

const historyModeCompress = "compress"

func agentImplSection(agent *AgentData) codegen.Section {
	return codegen.MustJenniferSection("agent-impl", func(stmt *jen.Statement) {
		emitAgentConstants(stmt, agent)
		emitAgentStruct(stmt, agent)
		emitAgentConstructor(stmt, agent)
		emitAgentWorker(stmt)
		emitAgentRoute(stmt, agent)
		emitAgentClient(stmt)
	})
}

func agentConfigSection(agent *AgentData) codegen.Section {
	return codegen.MustJenniferSection("agent-config", func(stmt *jen.Statement) {
		emitAgentConfigConstants(stmt, agent)
		emitAgentConfigStruct(stmt, agent)
		emitAgentConfigValidate(stmt, agent)
		emitAgentConfigWithMCPCaller(stmt, agent)
	})
}

func emitAgentConstants(stmt *jen.Statement, agent *AgentData) {
	stmt.Comment("AgentID is the fully-qualified identifier for this agent.").Line()
	stmt.Const().Id("AgentID").Id("agent").Dot("Ident").Op("=").Lit(agent.ID)
	stmt.Line()

	stmt.Comment("Workflow and activity identifiers for this agent.").Line()
	stmt.Const().Defs(
		jen.Comment("WorkflowName is the fully-qualified workflow identifier registered with the engine.").Line().
			Id("WorkflowName").Op("=").Lit(agent.Runtime.Workflow.Name),
		jen.Line().Comment("DefaultTaskQueue is the engine queue this agent polls for workflow and activity tasks.").Line().
			Id("DefaultTaskQueue").Op("=").Lit(agent.Runtime.Workflow.Queue),
		jen.Line().Comment("PlanActivity is the activity name that runs the initial planning turn.").Line().
			Id("PlanActivity").Op("=").Lit(agent.Runtime.PlanActivity.Name),
		jen.Line().Comment("ResumeActivity is the activity name that runs the resume turn after tool execution.").Line().
			Id("ResumeActivity").Op("=").Lit(agent.Runtime.ResumeActivity.Name),
		jen.Line().Comment("ExecuteToolActivity is the activity name used to execute tools via the engine.").Line().
			Id("ExecuteToolActivity").Op("=").Lit(agent.Runtime.ExecuteTool.Name),
	)
	stmt.Line()
}

func emitAgentStruct(stmt *jen.Statement, agent *AgentData) {
	codegen.Doc(stmt, agent.StructName+" wraps the planner implementation for agent \""+agent.Name+"\".")
	stmt.Type().Id(agent.StructName).Struct(
		jen.Id("Planner").Id("planner").Dot("Planner"),
	)
	stmt.Line()
}

func emitAgentConstructor(stmt *jen.Statement, agent *AgentData) {
	codegen.Doc(stmt, "New"+agent.StructName+" validates the configuration and constructs a "+agent.StructName+".")
	stmt.Func().Id("New"+agent.StructName).
		Params(jen.Id("cfg").Id(agent.ConfigType)).
		Params(jen.Op("*").Id(agent.StructName), jen.Error()).
		Block(
			jen.If(jen.Id("err").Op(":=").Id("cfg").Dot("Validate").Call(), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
			jen.Return(jen.Op("&").Id(agent.StructName).Values(jen.Dict{
				jen.Id("Planner"): jen.Id("cfg").Dot("Planner"),
			}), jen.Nil()),
		)
	stmt.Line()
}

func emitAgentWorker(stmt *jen.Statement) {
	codegen.Doc(stmt, "NewWorker returns a per-agent worker configuration. Engines that support\nworkers (e.g., Temporal) use this to bind the agent's workflow and activities\nto a specific queue. Supplying no options uses the generated default queue.")
	stmt.Func().Id("NewWorker").
		Params(jen.Id("opts").Op("...").Id("runtime").Dot("WorkerOption")).
		Id("runtime").Dot("WorkerConfig").
		Block(
			jen.Var().Id("cfg").Id("runtime").Dot("WorkerConfig"),
			jen.For(jen.List(jen.Id("_"), jen.Id("o")).Op(":=").Range().Id("opts")).Block(
				jen.If(jen.Id("o").Op("!=").Nil()).Block(
					jen.Id("o").Call(jen.Op("&").Id("cfg")),
				),
			),
			jen.Return(jen.Id("cfg")),
		)
	stmt.Line()
}

func emitAgentRoute(stmt *jen.Statement, agent *AgentData) {
	codegen.Doc(stmt, "Route returns the minimal route required to construct a client in a\ncaller process without registering the agent locally.")
	stmt.Func().Id("Route").Params().Id("runtime").Dot("AgentRoute").Block(
		jen.Return(jen.Id("runtime").Dot("AgentRoute").Values(jen.Dict{
			jen.Id("ID"):               jen.Id("AgentID"),
			jen.Id("WorkflowName"):     jen.Id("WorkflowName"),
			jen.Id("DefaultTaskQueue"): jen.Lit(agent.Runtime.Workflow.Queue),
		})),
	)
	stmt.Line()
}

func emitAgentClient(stmt *jen.Statement) {
	codegen.Doc(stmt, "NewClient returns a runtime.AgentClient bound to this agent. In caller\nprocesses that do not register the agent locally, this uses ClientMeta to\nconstruct a client that can start workflows against remote workers.")
	stmt.Func().Id("NewClient").
		Params(jen.Id("rt").Op("*").Id("runtime").Dot("Runtime")).
		Id("runtime").Dot("AgentClient").
		Block(
			jen.Return(jen.Id("rt").Dot("MustClientFor").Call(jen.Id("Route").Call())),
		)
	stmt.Line()
}

func emitAgentConfigConstants(stmt *jen.Statement, agent *AgentData) {
	if len(agent.MCPToolsets) == 0 {
		return
	}
	stmt.Const().DefsFunc(func(g *jen.Group) {
		for _, ts := range agent.MCPToolsets {
			g.Comment(ts.ConstName + " uniquely identifies the " + ts.QualifiedName + " MCP toolset binding.").Line()
			g.Id(ts.ConstName).Op("=").Lit(ts.QualifiedName)
		}
	})
	stmt.Line()
}

func emitAgentConfigStruct(stmt *jen.Statement, agent *AgentData) {
	codegen.Doc(stmt, agent.ConfigType+" configures the "+agent.StructName+" agent package.")
	stmt.Type().Id(agent.ConfigType).StructFunc(func(g *jen.Group) {
		g.Comment("Planner provides the concrete planner implementation used by the agent.")
		g.Id("Planner").Id("planner").Dot("Planner")
		if agent.RunPolicy.History != nil && agent.RunPolicy.History.Mode == historyModeCompress {
			g.Comment("HistoryModel provides the model client used for history compression when a")
			g.Comment("Compress history policy is configured.")
			g.Id("HistoryModel").Id("model").Dot("Client")
		}
		if len(agent.MCPToolsets) > 0 {
			g.Comment("MCPCallers maps MCP toolset IDs to the callers that invoke them. A caller must be")
			g.Comment("provided for every toolset referenced via MCPToolset/Use.")
			g.Id("MCPCallers").Map(jen.String()).Id("mcpruntime").Dot("Caller")
		}
	})
	stmt.Line()
}

func emitAgentConfigValidate(stmt *jen.Statement, agent *AgentData) {
	codegen.Doc(stmt, "Validate ensures the configuration is usable.")
	stmt.Func().Params(jen.Id("c").Id(agent.ConfigType)).Id("Validate").Params().Error().BlockFunc(func(g *jen.Group) {
		g.If(jen.Id("c").Dot("Planner").Op("==").Nil()).Block(
			jen.Return(jen.Qual("errors", "New").Call(jen.Lit("planner is required"))),
		)
		if agent.RunPolicy.History != nil && agent.RunPolicy.History.Mode == historyModeCompress {
			g.If(jen.Id("c").Dot("HistoryModel").Op("==").Nil()).Block(
				jen.Return(jen.Qual("errors", "New").Call(jen.Lit("history model is required when Compress history policy is configured"))),
			)
		}
		if len(agent.MCPToolsets) > 0 {
			g.If(jen.Id("c").Dot("MCPCallers").Op("==").Nil()).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("mcp caller for %s is required"), jen.Id(agent.MCPToolsets[0].ConstName))),
			)
			for _, ts := range agent.MCPToolsets {
				g.If(jen.Id("c").Dot("MCPCallers").Index(jen.Id(ts.ConstName)).Op("==").Nil()).Block(
					jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("mcp caller for %s is required"), jen.Id(ts.ConstName))),
				)
			}
		}
		g.Return(jen.Nil())
	})
	stmt.Line()
}

func emitAgentConfigWithMCPCaller(stmt *jen.Statement, agent *AgentData) {
	if len(agent.MCPToolsets) == 0 {
		return
	}
	codegen.Doc(stmt, "WithMCPCaller adds or replaces the caller for the given MCP toolset ID and returns\nthe config pointer for chaining in builder-style initialization.")
	stmt.Func().Params(jen.Id("c").Op("*").Id(agent.ConfigType)).
		Id("WithMCPCaller").
		Params(jen.Id("id").String(), jen.Id("caller").Id("mcpruntime").Dot("Caller")).
		Op("*").Id(agent.ConfigType).
		Block(
			jen.If(jen.Id("c").Dot("MCPCallers").Op("==").Nil()).Block(
				jen.Id("c").Dot("MCPCallers").Op("=").Make(jen.Map(jen.String()).Id("mcpruntime").Dot("Caller")),
			),
			jen.Id("c").Dot("MCPCallers").Index(jen.Id("id")).Op("=").Id("caller"),
			jen.Return(jen.Id("c")),
		)
	stmt.Line()
}
