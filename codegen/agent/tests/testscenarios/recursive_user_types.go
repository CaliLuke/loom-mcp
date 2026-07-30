package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
	goaexpr "github.com/CaliLuke/loom/expr"
)

// RecursiveToolTypes returns a DSL with recursive user types in tool payload
// and result positions.
func RecursiveToolTypes() func() {
	return func() {
		API("alpha", func() {})

		var Node goaexpr.UserType
		Node = Type("Node", func() {
			Attribute("name", String, "Node name")
			Attribute("next", Node, "Next node")
			Required("name")
		})

		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				Use("graph", func() {
					Tool("walk", "Walk graph", func() {
						Args(Node)
						Return(Node)
					})
				})
			})
		})
	}
}
