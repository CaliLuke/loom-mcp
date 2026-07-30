package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/v2/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/v2/expr/agent"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/require"
)

func TestFromMemoryDSL(t *testing.T) {
	runDSL(t, func() {
		Toolset("memory", FromMemory(MemoryLongTerm(), MemoryVisibilityShared(), MemoryMaxResults(20)))
	})

	require.Len(t, agentsexpr.Root.Toolsets, 1)
	ts := agentsexpr.Root.Toolsets[0]
	require.Equal(t, "memory", ts.Name)
	require.NotNil(t, ts.Provider)
	require.Equal(t, agentsexpr.ProviderMemory, ts.Provider.Kind)
	require.Equal(t, 20, ts.Provider.MemoryMaxResults)
	require.Equal(t, []agentsexpr.MemoryToolSource{agentsexpr.MemoryToolSourceLongTerm}, ts.Provider.MemorySources)
	require.Equal(t, agentsexpr.MemoryVisibilityShared, ts.Provider.MemoryVisibility)
}

func TestPreloadMemoryDSL(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		Service("tasks", func() {
			Agent("planner", "Planner agent", func() {
				RunPolicy(func() {
					PreloadMemory(MemoryScopeCurrentRun(), MemoryMaxResults(5))
				})
			})
		})
	})

	policy := agentsexpr.Root.Agents[0].RunPolicy
	require.NotNil(t, policy.PreloadMemory)
	require.Equal(t, agentsexpr.MemoryScopeCurrentRun, policy.PreloadMemory.Scope)
	require.Equal(t, 5, policy.PreloadMemory.MaxResults)
}

func TestPreloadLongTermMemoryDSL(t *testing.T) {
	runDSL(t, func() {
		API("test", func() {})
		Service("tasks", func() {
			Agent("planner", "Planner agent", func() {
				RunPolicy(func() {
					PreloadLongTermMemory(MemoryVisibilityShared(), MemoryMaxResults(5))
				})
			})
		})
	})

	policy := agentsexpr.Root.Agents[0].RunPolicy
	require.NotNil(t, policy.PreloadLongTermMemory)
	require.Equal(t, agentsexpr.MemoryVisibilityShared, policy.PreloadLongTermMemory.Visibility)
	require.Equal(t, 5, policy.PreloadLongTermMemory.MaxResults)
}
