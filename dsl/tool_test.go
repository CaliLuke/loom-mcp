package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/expr/agent"
	mcpexpr "github.com/CaliLuke/loom-mcp/expr/mcp"
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

func runProjectionDSL(t *testing.T, dsl func()) {
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

	require.True(t, eval.Execute(dsl, nil), eval.Context.Error())
	require.NoError(t, eval.RunDSL())
}
