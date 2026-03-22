package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// MCPUse references an external MCP toolset using Toolset with FromMCP.
func MCPUse() func() {
	return func() {
		API("alpha", func() {})
		// Provider service referenced by FromMCP
		Service("calc", func() {})
		var CalcCore = Toolset(FromMCP("calc", "core"))
		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				Use(CalcCore)
			})
		})
	}
}
