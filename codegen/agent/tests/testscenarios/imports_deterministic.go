package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// ImportsDeterministic uses a user type with a custom package path to exercise alias stability.
func ImportsDeterministic() func() {
	return func() {
		API("alpha", func() {})
		var Doc = Type("Doc", func() {
			Meta("struct:pkg:path", "example.com/mod/gen/types")
			Attribute("id", String, "Identifier")
			Required("id")
		})
		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				Use("docs", func() {
					Tool("store", "Store", func() {
						Args(Doc)
						Return(Doc)
					})
				})
			})
		})
	}
}
