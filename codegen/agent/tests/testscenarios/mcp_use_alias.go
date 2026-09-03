package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// MCPUseAlias references an external MCP toolset through a local alias so the
// generator must keep definition-owned package names separate from provider
// metadata.
func MCPUseAlias() func() {
	return func() {
		API("alpha", func() {})
		// External provider service and inline schemas referenced by FromMCP.
		Service("calc", func() {
		})
		var CalcRemote = Toolset("calc-remote", FromMCP("calc", "core"), func() {
			Tool("ping", "Ping", func() {
				Args(String)
				Return(String)
			})
		})
		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				Use(CalcRemote)
			})
		})
	}
}
