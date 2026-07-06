package dsl_test

import (
	"testing"

	. "github.com/CaliLuke/loom-mcp/dsl"
	agentsexpr "github.com/CaliLuke/loom-mcp/expr/agent"
	. "github.com/CaliLuke/loom/dsl"
	"github.com/stretchr/testify/require"
)

func TestFromMemoryDSL(t *testing.T) {
	runDSL(t, func() {
		Toolset("memory", FromMemory(MemoryMaxResults(20)))
	})

	require.Len(t, agentsexpr.Root.Toolsets, 1)
	ts := agentsexpr.Root.Toolsets[0]
	require.Equal(t, "memory", ts.Name)
	require.NotNil(t, ts.Provider)
	require.Equal(t, agentsexpr.ProviderMemory, ts.Provider.Kind)
	require.Equal(t, 20, ts.Provider.MemoryMaxResults)
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
