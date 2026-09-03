package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// MCPUse references an external MCP toolset using Toolset with FromMCP.
func MCPUse() func() {
	return func() {
		API("alpha", func() {})
		// External provider service and inline schemas referenced by FromMCP.
		Service("calc", func() {
		})
		var CalcCore = Toolset(FromMCP("calc", "core"), func() {
			Tool("ping", "Ping", func() {
				Args(String)
				Return(String)
			})
		})
		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				Use(CalcCore)
			})
		})
	}
}
