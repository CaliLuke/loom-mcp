package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolsetExprValidateMemoryProvider(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "memory",
			Provider: &ProviderExpr{
				Kind:             ProviderMemory,
				MemoryMaxResults: 20,
				MemorySources:    []MemoryToolSource{MemoryToolSourceTranscript, MemoryToolSourceLongTerm},
				MemoryVisibility: MemoryVisibilityUser,
			},
		}
		require.NoError(t, ts.Validate())
	})

	t.Run("negative max results", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "memory",
			Provider: &ProviderExpr{
				Kind:             ProviderMemory,
				MemoryMaxResults: -1,
			},
		}
		require.ErrorContains(t, ts.Validate(), "MemoryMaxResults must be non-negative")
	})

	t.Run("inline tools rejected", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name:     "memory",
			Provider: &ProviderExpr{Kind: ProviderMemory},
			Tools:    []*ToolExpr{{Name: "load_memory"}},
		}
		require.ErrorContains(t, ts.Validate(), "FromMemory toolsets cannot declare inline tools")
	})

	t.Run("invalid source rejected", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "memory",
			Provider: &ProviderExpr{
				Kind:          ProviderMemory,
				MemorySources: []MemoryToolSource{"unknown"},
			},
		}
		require.ErrorContains(t, ts.Validate(), "unknown memory source")
	})

	t.Run("duplicate source rejected", func(t *testing.T) {
		ts := &ToolsetExpr{
			Name: "memory",
			Provider: &ProviderExpr{
				Kind:          ProviderMemory,
				MemorySources: []MemoryToolSource{MemoryToolSourceTranscript, MemoryToolSourceTranscript},
			},
		}
		require.ErrorContains(t, ts.Validate(), "duplicate memory source")
	})
}

func TestRunPolicyExprValidatePreloadMemory(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent: &AgentExpr{Name: "planner"},
			PreloadMemory: &MemoryPreloadExpr{
				Scope:      MemoryScopeCurrentRun,
				MaxResults: 5,
			},
		}
		require.NoError(t, policy.Validate())
	})

	t.Run("missing scope", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent:         &AgentExpr{Name: "planner"},
			PreloadMemory: &MemoryPreloadExpr{MaxResults: 5},
		}
		require.ErrorContains(t, policy.Validate(), "PreloadMemory requires a scope")
	})

	t.Run("negative max results", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent: &AgentExpr{Name: "planner"},
			PreloadMemory: &MemoryPreloadExpr{
				Scope:      MemoryScopeCurrentRun,
				MaxResults: -1,
			},
		}
		require.ErrorContains(t, policy.Validate(), "PreloadMemory MaxResults must be non-negative")
	})
}

func TestRunPolicyExprValidatePreloadLongTermMemory(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent: &AgentExpr{Name: "planner"},
			PreloadLongTermMemory: &LongTermMemoryPreloadExpr{
				Visibility: MemoryVisibilityShared,
				MaxResults: 5,
			},
		}
		require.NoError(t, policy.Validate())
	})

	t.Run("invalid visibility", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent: &AgentExpr{Name: "planner"},
			PreloadLongTermMemory: &LongTermMemoryPreloadExpr{
				Visibility: "unknown",
			},
		}
		require.ErrorContains(t, policy.Validate(), "unknown PreloadLongTermMemory visibility")
	})

	t.Run("negative max results", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent: &AgentExpr{Name: "planner"},
			PreloadLongTermMemory: &LongTermMemoryPreloadExpr{
				Visibility: MemoryVisibilityUser,
				MaxResults: -1,
			},
		}
		require.ErrorContains(t, policy.Validate(), "PreloadLongTermMemory MaxResults must be non-negative")
	})
}
