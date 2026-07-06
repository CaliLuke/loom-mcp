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
	require.Contains(t, registry, `PreloadMemory: &agentsruntime.MemoryPreloadPolicy{`)
	require.Contains(t, registry, `Scope: agentsruntime.MemoryScopeCurrentRun,`)
	require.Contains(t, registry, `MaxResults: 5,`)
}
