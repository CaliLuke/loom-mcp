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
