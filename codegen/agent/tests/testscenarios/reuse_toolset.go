package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// ReUse declares a top-level toolset and references it via Use.
func ReUse() func() {
	return func() {
		API("alpha", func() {})
		var Shared = Toolset("shared", func() {
			Tool("ping", "Ping", func() {})
		})
		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				Use(Shared)
			})
		})
	}
}
