package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// MemoryToolset references runtime memory tools and enables current-run preload.
func MemoryToolset() func() {
	return func() {
		API("alpha", func() {})
		var Memory = Toolset("memory", FromMemory(MemoryMaxResults(20)))
		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				RunPolicy(func() {
					PreloadMemory(MemoryScopeCurrentRun(), MemoryMaxResults(5))
				})
				Use(Memory)
			})
		})
	}
}
