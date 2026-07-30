package testscenarios

import (
	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	. "github.com/CaliLuke/loom/dsl"
)

// MemoryToolset references runtime memory tools and enables current-run preload.
func MemoryToolset() func() {
	return func() {
		API("alpha", func() {})
		var Memory = Toolset("memory", FromMemory(MemoryMaxResults(20)))
		var TranscriptMemory = Toolset("transcript_memory", FromMemory(MemoryTranscript(), MemoryMaxResults(10)))
		var IndexedMemory = Toolset("indexed_memory", FromMemory(MemoryIndexedTranscript(), MemoryMaxResults(11)))
		var LongTermMemory = Toolset("long_term_memory", FromMemory(MemoryLongTerm(), MemoryVisibilityUser(), MemoryMaxResults(12)))
		var MixedMemory = Toolset("mixed_memory", FromMemory(MemoryTranscript(), MemoryLongTerm(), MemoryVisibilityShared(), MemoryMaxResults(13)))
		Service("alpha", func() {
			Agent("scribe", "Doc helper", func() {
				RunPolicy(func() {
					PreloadMemory(MemoryScopeCurrentRun(), MemoryMaxResults(5))
					PreloadLongTermMemory(MemoryVisibilityUser(), MemoryMaxResults(6))
				})
				Use(Memory)
				Use(TranscriptMemory)
				Use(IndexedMemory)
				Use(LongTermMemory)
				Use(MixedMemory)
			})
		})
	}
}
