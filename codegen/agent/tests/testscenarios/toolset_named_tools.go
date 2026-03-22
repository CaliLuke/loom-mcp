package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// ToolsetNamedTools creates a scenario where the toolset is named "tools",
// which could conflict with the runtime tools package import.
func ToolsetNamedTools() func() {
	return func() {
		Service("alpha", func() {
			Agent("helper", "Helper agent", func() {
				// Toolset named "tools" - this should not conflict with
				// github.com/CaliLuke/loom-mcp/runtime/agent/tools import
				Use("tools", func() {
					Tool("do_something", "Does something", func() {
						Args(func() {
							Attribute("input", String, "Input value")
							Required("input")
						})
						Return(func() {
							Attribute("output", String, "Output value")
							Required("output")
						})
					})
				})
			})
		})
	}
}
