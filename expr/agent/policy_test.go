package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunPolicyExprValidateCapsAndTiming(t *testing.T) {
	t.Run("zero values are unset", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent:       &AgentExpr{Name: "planner"},
			DefaultCaps: &CapsExpr{},
		}

		require.NoError(t, policy.Validate())
	})

	t.Run("negative max tool calls", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent: &AgentExpr{Name: "planner"},
			DefaultCaps: &CapsExpr{
				MaxToolCalls: -1,
			},
		}

		require.ErrorContains(t, policy.Validate(), "MaxToolCalls must be non-negative")
	})

	t.Run("negative consecutive failed tool calls", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent: &AgentExpr{Name: "planner"},
			DefaultCaps: &CapsExpr{
				MaxConsecutiveFailedToolCall: -1,
			},
		}

		require.ErrorContains(t, policy.Validate(), "MaxConsecutiveFailedToolCalls must be non-negative")
	})

	t.Run("negative time budget", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent:      &AgentExpr{Name: "planner"},
			TimeBudget: -time.Second,
		}

		require.ErrorContains(t, policy.Validate(), "TimeBudget must be non-negative")
	})

	t.Run("negative plan timeout", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent:       &AgentExpr{Name: "planner"},
			PlanTimeout: -time.Second,
		}

		require.ErrorContains(t, policy.Validate(), "PlanTimeout must be non-negative")
	})

	t.Run("negative tool timeout", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent:       &AgentExpr{Name: "planner"},
			ToolTimeout: -time.Second,
		}

		require.ErrorContains(t, policy.Validate(), "ToolTimeout must be non-negative")
	})
}
