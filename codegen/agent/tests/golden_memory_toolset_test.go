package tests

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/codegen/agent/tests/testscenarios"
	"github.com/stretchr/testify/require"
)

func TestGolden_MemoryToolset_RegisterUsedToolsetsAndPreload(t *testing.T) {
	files := buildAndGenerate(t, testscenarios.MemoryToolset())
	registry := fileContent(t, files, "gen/alpha/agents/scribe/registry.go")

	require.Contains(t, registry, `agentsruntime.NewMemoryToolsetRegistration(agentsruntime.MemoryToolsetConfig{`)
	require.Contains(t, registry, `Store: rt.Memory,`)
	require.Contains(t, registry, `Searcher: rt.MemorySearcher,`)
	require.Contains(t, registry, `Name: "alpha.memory",`)
	require.Contains(t, registry, `MaxResults: 20,`)
	require.NotContains(t, registry, `Name: "alpha.memory",
            Sources:`)
	require.Contains(t, registry, `Name: "alpha.transcript_memory",`)
	require.Contains(t, registry, `memory.ToolSourceTranscript`)
	require.Contains(t, registry, `Name: "alpha.indexed_memory",`)
	require.Contains(t, registry, `memory.ToolSourceIndexedTranscript`)
	require.Contains(t, registry, `Name: "alpha.long_term_memory",`)
	require.Contains(t, registry, `Service: rt.MemoryService,`)
	require.Contains(t, registry, `ScopeResolver: rt.MemoryScopeResolver,`)
	require.Contains(t, registry, `memory.ToolSourceLongTerm`)
	require.Contains(t, registry, `Visibility: memory.VisibilityUser,`)
	require.Contains(t, registry, `Name: "alpha.mixed_memory",`)
	require.Contains(t, registry, `Visibility: memory.VisibilityShared,`)
	require.Contains(t, registry, `PreloadMemory: &agentsruntime.MemoryPreloadPolicy{`)
	require.Contains(t, registry, `Scope: agentsruntime.MemoryScopeCurrentRun,`)
	require.Contains(t, registry, `MaxResults: 5,`)
	require.Contains(t, registry, `PreloadLongTermMemory: &agentsruntime.LongTermMemoryPreloadPolicy{`)
	require.Contains(t, registry, `Visibility: memory.VisibilityUser,`)
	require.Contains(t, registry, `MaxResults: 6,`)
}
