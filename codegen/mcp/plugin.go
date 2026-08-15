package codegen

import (
	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
)

// PrepareServices validates MCP-enabled services before generation. MCP no
// longer mutates Goa transport expressions: the official SDK owns the MCP wire
// transport, while explicitly designed HTTP and JSON-RPC transports remain
// independent application surfaces.
func PrepareServices(_ string, roots []eval.Root) error {
	source := collectSourceSnapshot(roots)
	for _, root := range roots {
		r, ok := root.(*expr.RootExpr)
		if !ok {
			continue
		}

		for _, svc := range r.Services {
			if mcpexpr.Root != nil && mcpexpr.Root.HasMCP(svc) {
				if err := validatePureMCPService(svc, mcpexpr.Root.GetMCP(svc), source); err != nil {
					return err
				}
			}
		}
	}

	return nil
}
