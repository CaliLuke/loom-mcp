package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// MultiServiceExample defines same-named agents in two services to exercise
// service-scoped example scaffolding.
func MultiServiceExample() func() {
	return func() {
		API("multi", func() {})
		Service("alpha", func() {
			Agent("scribe", "Alpha helper", func() {})
		})
		Service("beta", func() {
			Agent("scribe", "Beta helper", func() {})
		})
	}
}
