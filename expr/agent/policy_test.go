package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

	t.Run("negative recovery turns", func(t *testing.T) {
		policy := &RunPolicyExpr{
			Agent: &AgentExpr{Name: "planner"},
			DefaultCaps: &CapsExpr{
				MaxRecoveryTurns: -1,
			},
		}

		require.ErrorContains(t, policy.Validate(), "MaxRecoveryTurns must be non-negative")
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

func TestRunPolicyExprValidateHistory(t *testing.T) {
	cases := []struct {
		name    string
		history *HistoryExpr
		want    []string
	}{
		{
			name:    "mode is required",
			history: &HistoryExpr{},
			want:    []string{"history policy must specify a mode"},
		},
		{
			name:    "recent turn count is required",
			history: &HistoryExpr{Mode: HistoryModeKeepRecent},
			want:    []string{"KeepRecentTurns requires a positive turn count"},
		},
		{
			name:    "unknown mode",
			history: &HistoryExpr{Mode: "archive"},
			want:    []string{`unknown history mode "archive"`},
		},
		{
			name:    "compression requires trigger and retention",
			history: &HistoryExpr{Mode: HistoryModeCompress},
			want: []string{
				"compression requires CompressAtTurns or CompressAtMaxInputTokens",
				"compression requires KeepMaxTurns or KeepMaxInputTokens",
			},
		},
		{
			name: "negative turn trigger",
			history: &HistoryExpr{
				Mode:                     HistoryModeCompress,
				CompressAtTurns:          -1,
				CompressAtMaxInputTokens: 100,
				KeepMaxInputTokens:       20,
			},
			want: []string{"CompressAtTurns must be positive when set"},
		},
		{
			name: "negative token trigger",
			history: &HistoryExpr{
				Mode:                     HistoryModeCompress,
				CompressAtTurns:          10,
				CompressAtMaxInputTokens: -1,
				KeepMaxTurns:             2,
			},
			want: []string{"CompressAtMaxInputTokens must be positive when set"},
		},
		{
			name: "negative retained turns",
			history: &HistoryExpr{
				Mode:                     HistoryModeCompress,
				CompressAtMaxInputTokens: 100,
				KeepMaxTurns:             -1,
				KeepMaxInputTokens:       20,
			},
			want: []string{"KeepMaxTurns must be positive when set"},
		},
		{
			name: "negative retained tokens",
			history: &HistoryExpr{
				Mode:               HistoryModeCompress,
				CompressAtTurns:    10,
				KeepMaxTurns:       2,
				KeepMaxInputTokens: -1,
			},
			want: []string{"KeepMaxInputTokens must be positive when set"},
		},
		{
			name: "retained turns must be below trigger",
			history: &HistoryExpr{
				Mode:            HistoryModeCompress,
				CompressAtTurns: 10,
				KeepMaxTurns:    10,
			},
			want: []string{"KeepMaxTurns must be less than CompressAtTurns"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := &RunPolicyExpr{
				Agent:   &AgentExpr{Name: "planner"},
				History: tc.history,
			}

			err := policy.Validate()
			for _, want := range tc.want {
				assert.ErrorContains(t, err, want)
			}
		})
	}
}

func TestRunPolicyExprValidateHistoryAcceptsValidModes(t *testing.T) {
	cases := []struct {
		name    string
		history *HistoryExpr
	}{
		{
			name:    "recent turns",
			history: &HistoryExpr{Mode: HistoryModeKeepRecent, KeepRecent: 1},
		},
		{
			name: "compress by turns",
			history: &HistoryExpr{
				Mode:            HistoryModeCompress,
				CompressAtTurns: 10,
				KeepMaxTurns:    9,
			},
		},
		{
			name: "compress by tokens",
			history: &HistoryExpr{
				Mode:                     HistoryModeCompress,
				CompressAtMaxInputTokens: 100,
				KeepMaxInputTokens:       20,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := &RunPolicyExpr{
				Agent:   &AgentExpr{Name: "planner"},
				History: tc.history,
			}

			require.NoError(t, policy.Validate())
		})
	}
}
