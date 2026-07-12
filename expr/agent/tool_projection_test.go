package agent

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/expr/mcp"
	goaexpr "github.com/CaliLuke/loom/expr"
	"github.com/stretchr/testify/require"
)

func TestToolSurfaceProjectionValidation(t *testing.T) {
	preserveGlobalRoots(t)

	service := &goaexpr.ServiceExpr{Name: "assistant"}
	method := &goaexpr.MethodExpr{Name: "lookup", Service: service}
	service.Methods = []*goaexpr.MethodExpr{method}
	goaexpr.Root = &goaexpr.RootExpr{Services: []*goaexpr.ServiceExpr{service}}
	mcp.Root = &mcp.RootExpr{
		MCPServers: map[string]*mcp.MCPExpr{
			"assistant": {
				Name:    "assistant-mcp",
				Version: "1.0.0",
				Service: service,
			},
		},
	}
	agent := &AgentExpr{Name: "planner", Service: service}
	toolset := &ToolsetExpr{Name: "lookup-tools", Agent: agent}
	tool := &ToolExpr{
		Name:        "lookup_tool",
		Description: "Lookup",
		Toolset:     toolset,
		Surfaces: []ToolSurface{
			ToolSurfaceAgentRuntime,
			ToolSurfaceMCP,
		},
		MCPPlacement: &ToolMCPPlacementExpr{
			Service:   "assistant",
			MCPServer: "assistant-mcp",
		},
	}
	tool.RecordBinding("assistant", "lookup")
	toolset.Tools = []*ToolExpr{tool}
	agent.Used = &ToolsetGroupExpr{Agent: agent, Toolsets: []*ToolsetExpr{toolset}}

	require.NoError(t, tool.Validate())
	require.NoError(t, (&RootExpr{Agents: []*AgentExpr{agent}}).Validate())
	require.Equal(t, method, tool.Method)

	cases := []struct {
		name   string
		mutate func(*ToolExpr)
		err    string
	}{
		{
			name: "duplicate surface",
			mutate: func(tool *ToolExpr) {
				tool.Surfaces = append(tool.Surfaces, ToolSurfaceMCP)
			},
			err: "Expose declares surface",
		},
		{
			name: "mcp without runtime",
			mutate: func(tool *ToolExpr) {
				tool.Surfaces = []ToolSurface{ToolSurfaceMCP}
			},
			err: "MCPSurface requires AgentRuntime",
		},
		{
			name: "mcp without placement",
			mutate: func(tool *ToolExpr) {
				tool.MCPPlacement = nil
			},
			err: "MCPPlacement is required",
		},
		{
			name: "placement without mcp",
			mutate: func(tool *ToolExpr) {
				tool.Surfaces = []ToolSurface{ToolSurfaceAgentRuntime}
			},
			err: "MCPPlacement requires MCPSurface",
		},
		{
			name: "confirmation unsupported",
			mutate: func(tool *ToolExpr) {
				tool.Confirmation = &ToolConfirmationExpr{}
			},
			err: "Confirmation is not supported",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate := cloneProjectionTool(tool)
			tc.mutate(candidate)
			require.ErrorContains(t, candidate.Validate(), tc.err)
		})
	}
}

func TestToolSurfaceProjectionRootValidation(t *testing.T) {
	preserveGlobalRoots(t)

	service := &goaexpr.ServiceExpr{Name: "assistant"}
	otherService := &goaexpr.ServiceExpr{Name: "other"}
	method := &goaexpr.MethodExpr{Name: "lookup", Service: service}
	service.Methods = []*goaexpr.MethodExpr{method}
	goaexpr.Root = &goaexpr.RootExpr{Services: []*goaexpr.ServiceExpr{service, otherService}}
	mcp.Root = &mcp.RootExpr{
		MCPServers: map[string]*mcp.MCPExpr{
			"assistant": {
				Name:    "assistant-mcp",
				Version: "1.0.0",
				Service: service,
				Tools: []*mcp.ToolExpr{
					{Name: "method_collision", Description: "Existing method-level tool"},
				},
			},
			"other": {
				Name:    "other-mcp",
				Version: "1.0.0",
				Service: otherService,
			},
		},
	}

	cases := []struct {
		name  string
		tools []*ToolExpr
		err   string
	}{
		{
			name: "unresolved placement",
			tools: []*ToolExpr{
				projectedTool("projected", "missing", "missing-mcp"),
			},
			err: "MCPPlacement could not resolve service",
		},
		{
			name: "cross service placement",
			tools: []*ToolExpr{
				projectedTool("projected", "other", "other-mcp"),
			},
			err: "must match bound service",
		},
		{
			name: "projected name collides with method tool",
			tools: []*ToolExpr{
				projectedTool("method_collision", "assistant", "assistant-mcp"),
			},
			err: "duplicates method-level MCP tool",
		},
		{
			name: "projected name collides with projected tool",
			tools: []*ToolExpr{
				projectedTool("projected", "assistant", "assistant-mcp"),
				projectedTool("projected", "assistant", "assistant-mcp"),
			},
			err: "duplicates projected tool",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := &AgentExpr{Name: "planner", Service: service}
			toolset := &ToolsetExpr{Name: "lookup-tools", Agent: agent, Tools: tc.tools}
			for _, tool := range tc.tools {
				tool.Toolset = toolset
				require.NoError(t, tool.Validate())
			}
			agent.Used = &ToolsetGroupExpr{Agent: agent, Toolsets: []*ToolsetExpr{toolset}}
			require.ErrorContains(t, (&RootExpr{Agents: []*AgentExpr{agent}}).Validate(), tc.err)
		})
	}
}

func cloneProjectionTool(tool *ToolExpr) *ToolExpr {
	clone := *tool
	clone.Surfaces = append([]ToolSurface(nil), tool.Surfaces...)
	if tool.MCPPlacement != nil {
		placement := *tool.MCPPlacement
		clone.MCPPlacement = &placement
	}
	clone.Method = nil
	clone.RecordBinding("assistant", "lookup")
	return &clone
}

func projectedTool(name, placementService, placementServer string) *ToolExpr {
	tool := &ToolExpr{
		Name:        name,
		Description: "Lookup",
		Surfaces: []ToolSurface{
			ToolSurfaceAgentRuntime,
			ToolSurfaceMCP,
		},
		MCPPlacement: &ToolMCPPlacementExpr{
			Service:   placementService,
			MCPServer: placementServer,
		},
	}
	tool.RecordBinding("assistant", "lookup")
	return tool
}
