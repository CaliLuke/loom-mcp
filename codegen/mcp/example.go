package codegen

import (
	"fmt"

	mcpexpr "github.com/CaliLuke/loom-mcp/v2/expr/mcp"
	"github.com/CaliLuke/loom/codegen"
	"github.com/CaliLuke/loom/eval"
	"github.com/CaliLuke/loom/expr"
)

// PrepareExample validates MCP mappings without injecting a native transport
// into Loom's example generator. Applications mount the generated SDKServer at
// their chosen Streamable HTTP path.
func PrepareExample(genpkg string, roots []eval.Root) error {
	source := collectSourceSnapshot(roots)
	for _, root := range roots {
		r, ok := root.(*expr.RootExpr)
		if !ok {
			continue
		}
		for _, svc := range r.Services {
			if !mcpexpr.Root.HasMCP(svc) {
				continue
			}
			mcp := mcpexpr.Root.GetMCP(svc)
			if err := validatePureMCPService(svc, mcp, source); err != nil {
				return err
			}
			if _, err := ProjectedToolInventory(genpkg, roots, svc.Name, mcp.Name); err != nil {
				return fmt.Errorf("build projected tool inventory for %s.%s: %w", svc.Name, mcp.Name, err)
			}
		}
	}
	return nil
}

// ModifyExampleFiles leaves Loom-owned application examples unchanged. MCP
// applications explicitly mount the generated SDKServer.
func ModifyExampleFiles(_ string, _ []eval.Root, files []*codegen.File) ([]*codegen.File, error) {
	return files, nil
}
