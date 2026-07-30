package codegen

import (
	"slices"
	"strings"

	agentsExpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	"github.com/CaliLuke/loom/eval"
)

// ProjectedMCPTools returns canonical generated tool data for toolset tools
// projected into the named MCP server.
func ProjectedMCPTools(genpkg string, roots []eval.Root, serviceName string, mcpServer string) ([]*ToolData, error) {
	data, err := buildGeneratorData(genpkg, roots)
	if err != nil {
		return nil, err
	}
	var tools []*ToolData
	for _, service := range data.Services {
		for _, agent := range service.Agents {
			for _, tool := range agent.Tools {
				if !tool.MCPProjected {
					continue
				}
				if tool.MCPPlacementService != serviceName || tool.MCPPlacementServer != mcpServer {
					continue
				}
				if !slices.Contains(tool.Surfaces, agentsExpr.ToolSurfaceMCP) {
					continue
				}
				tools = append(tools, tool)
			}
		}
	}
	slices.SortFunc(tools, func(a, b *ToolData) int {
		if a.Toolset.Name != b.Toolset.Name {
			return strings.Compare(a.Toolset.Name, b.Toolset.Name)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return tools, nil
}
