package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// ToolsetNamedPolicy declares an agent using a toolset named "policy". The
// specs aggregator imports the runtime policy package, so the toolset specs
// package must receive a distinct import alias.
func ToolsetNamedPolicy() func() {
	return func() {
		API("alpha", func() {})
		Service("alpha", func() {
			Agent("helper", "Helper agent", func() {
				Use("policy", func() {
					Tool("evaluate", "Evaluate a policy", func() {
						Args(func() {
							Attribute("name", String, "Policy name")
							Required("name")
						})
						Return(func() {
							Attribute("allowed", Boolean, "Decision")
						})
					})
				})
			})
		})
	}
}
