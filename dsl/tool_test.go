package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/CaliLuke/loom/eval"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

func TestToolSurfaceProjectionDSL(t *testing.T) {
	runProjectionDSL(t, func() {
		API("test", func() {})
		Service("assistant", func() {
			MCP("assistant-mcp", "1.0.0")
			Method("lookup", func() {
				Payload(String)
				Result(String)
				Tool("lookup", "Lookup through MCP only", Expose(MCPSurface))
			})
			Agent("planner", "Planner", func() {
				Use("lookup-tools", func() {
					Tool("lookup_tool", "Lookup through runtime and MCP", func() {
						BindTo("assistant", "lookup")
						Expose(AgentRuntime, MCPSurface)
						MCPPlacement("assistant", "assistant-mcp")
					})
				})
			})
		})
	})

	tool := agentsexpr.Root.Agents[0].Used.Toolsets[0].Tools[0]
	require.Equal(t, []agentsexpr.ToolSurface{
		agentsexpr.ToolSurfaceAgentRuntime,
		agentsexpr.ToolSurfaceMCP,
	}, tool.Surfaces)
	require.NotNil(t, tool.MCPPlacement)
	require.Equal(t, "assistant", tool.MCPPlacement.Service)
	require.Equal(t, "assistant-mcp", tool.MCPPlacement.MCPServer)

	mcpTool := mcpexpr.Root.MCPServers["assistant"].Tools[0]
	require.Equal(t, []string{"mcp"}, mcpTool.ExposedSurfaces)
	require.Empty(t, mcpTool.MCPPlacementService)
	require.Empty(t, mcpTool.MCPPlacementServer)
}

func TestToolReportsInvalidArgumentType(t *testing.T) {
	setupProjectionDSL(t)

	require.True(t, eval.Execute(func() {
		API("test", func() {})
		Service("assistant", func() {
			MCP("assistant-mcp", "1.0.0")
			Method("lookup", func() {
				Tool("lookup", 42)
			})
		})
	}, nil), eval.Context.Error())

	err := eval.RunDSL()
	require.ErrorContains(t, err, "cannot use 42 (type int) as type description, DSL function, or MCP tool option")
}

func TestExposeRejectsToolsetBlockContext(t *testing.T) {
	err := runInvalidProjectionDSL(t, func() {
		API("test", func() {})
		Toolset("bad-tools", func() {
			Expose(MCPSurface)
		})
	})

	require.Contains(t, err, "invalid use of Expose in toolset \"bad-tools\"")
}

func TestMCPPlacementRejectsToolsetBlockContext(t *testing.T) {
	err := runInvalidProjectionDSL(t, func() {
		API("test", func() {})
		Toolset("bad-tools", func() {
			MCPPlacement("assistant", "assistant-mcp")
		})
	})

	require.Contains(t, err, "invalid use of MCPPlacement in toolset \"bad-tools\"")
}

func TestToolRequiresMCPForMethodContext(t *testing.T) {
	err := runInvalidProjectionDSL(t, func() {
		API("test", func() {})
		Service("assistant", func() {
			Method("lookup", func() {
				Tool("lookup", "Lookup")
			})
		})
	})

	require.Contains(t, err, `Tool requires service "assistant" to declare MCP in service "assistant" method "lookup"`)
}

func setupProjectionDSL(t *testing.T) {
	t.Helper()

	eval.Reset()
	goaexpr.Root = new(goaexpr.RootExpr)
	goaexpr.GeneratedResultTypes = new(goaexpr.ResultTypesRoot)
	require.NoError(t, eval.Register(goaexpr.Root))
	require.NoError(t, eval.Register(goaexpr.GeneratedResultTypes))
	agentsexpr.Root = &agentsexpr.RootExpr{}
	require.NoError(t, eval.Register(agentsexpr.Root))
	mcpexpr.Root = mcpexpr.NewRoot()
	require.NoError(t, eval.Register(mcpexpr.Root))

	goaexpr.Root.API = goaexpr.NewAPIExpr("test", func() {})
	goaexpr.Root.API.Servers = []*goaexpr.ServerExpr{goaexpr.Root.API.DefaultServer()}
}

func runProjectionDSL(t *testing.T, dsl func()) {
	t.Helper()

	setupProjectionDSL(t)

	require.True(t, eval.Execute(dsl, nil), eval.Context.Error())
	require.NoError(t, eval.RunDSL())
}

func runInvalidProjectionDSL(t *testing.T, dsl func()) string {
	t.Helper()

	setupProjectionDSL(t)

	require.True(t, eval.Execute(dsl, nil), eval.Context.Error())
	require.Error(t, eval.RunDSL())
	return eval.Context.Error()
}
