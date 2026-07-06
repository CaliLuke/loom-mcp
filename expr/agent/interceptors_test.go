package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunPolicyExprValidateInterceptors(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent:        &AgentExpr{Name: "assistant"},
			Interceptors: []string{"audit", "safety"},
		}
		require.NoError(t, policy.Validate())
	})

	t.Run("empty", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent:        &AgentExpr{Name: "assistant"},
			Interceptors: []string{"audit", ""},
		}
		require.ErrorContains(t, policy.Validate(), "interceptor id must be non-empty")
	})

	t.Run("duplicate", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent:        &AgentExpr{Name: "assistant"},
			Interceptors: []string{"audit", "audit"},
		}
		require.ErrorContains(t, policy.Validate(), "duplicate interceptor id")
	})
}
