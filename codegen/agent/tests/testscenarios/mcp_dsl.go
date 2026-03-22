package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// MCPDSL references an external MCP toolset using the Toolset with FromMCP DSL.
func MCPDSL() func() {
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
