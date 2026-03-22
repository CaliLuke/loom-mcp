package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// ExportsSimple declares an agent that exports a single toolset with one tool.
func ExportsSimple() func() {
	return func() {
		API("alpha", func() {})
		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				Export("search", func() {
					Tool("find", "Find documents", func() {
						Args(String)
						Return(String)
					})
				})
			})
		})
	}
}
