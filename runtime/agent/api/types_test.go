package api

import (
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func TestPlanActivityOutput_UnmarshalJSON(t *testing.T) {
	t.Run("references", func(t *testing.T) {
		out := PlanActivityOutput{IndexHead: "head", TranscriptRef: "turn", ToolCallRefs: []runstore.Hash{"call"}, AwaitCallRefs: map[string]runstore.Hash{"await": "question"}}
		data, err := json.Marshal(out)
		require.NoError(t, err)
		var decoded PlanActivityOutput
		require.NoError(t, json.Unmarshal(data, &decoded))
		encoded, err := json.Marshal(decoded)
		require.NoError(t, err)
		require.JSONEq(t, string(data), string(encoded))
	})
	t.Run("policy fields", func(t *testing.T) {
		const payload = `{
			"Result": null,
			"Transcript": [],
			"ToolPolicyActive": true,
			"AllowedTools": ["search.web", "memory.lookup"],
			"PolicyCaps": {
				"MaxToolCalls": 5,
				"RemainingToolCalls": 3,
				"MaxRecoveryTurns": 2,
				"RemainingRecoveryTurns": 1
			}
		}`

		var out PlanActivityOutput
		require.NoError(t, json.Unmarshal([]byte(payload), &out))
		require.True(t, out.ToolPolicyActive)
		require.Equal(t, []tools.Ident{"search.web", "memory.lookup"}, out.AllowedTools)
		require.Equal(t, policy.CapsState{
			MaxToolCalls:           5,
			RemainingToolCalls:     3,
			MaxRecoveryTurns:       2,
			RemainingRecoveryTurns: 1,
		}, out.PolicyCaps)
	})

	t.Run("historical recovery caps", func(t *testing.T) {
		const payload = `{
			"Result": null,
			"Transcript": [],
			"PolicyCaps": {
				"MaxToolCalls": 5,
				"RemainingToolCalls": 3,
				"MaxConsecutiveFailedToolCalls": 4,
				"RemainingConsecutiveFailedToolCalls": 2
			}
		}`

		var out PlanActivityOutput
		require.NoError(t, json.Unmarshal([]byte(payload), &out))
		require.Equal(t, policy.CapsState{
			MaxToolCalls:           5,
			RemainingToolCalls:     3,
			MaxRecoveryTurns:       4,
			RemainingRecoveryTurns: 2,
		}, out.PolicyCaps)
	})
}

func TestPolicyOverridesUnmarshalJSONMigratesHistoricalRecoveryCap(t *testing.T) {
	t.Parallel()

	const payload = `{
		"MaxToolCalls": 8,
		"MaxConsecutiveFailedToolCalls": 5,
		"TimeBudget": 2000000000,
		"PlanTimeout": 3000000000,
		"ToolTimeout": 4000000000,
		"PerToolTimeout": {"svc.read": 5000000000},
		"FinalizerGrace": 6000000000,
		"InterruptsAllowed": true
	}`
	var overrides PolicyOverrides
	require.NoError(t, json.Unmarshal([]byte(payload), &overrides))
	require.Equal(t, 8, overrides.MaxToolCalls)
	require.Equal(t, 5, overrides.MaxRecoveryTurns)
	require.Equal(t, 2*time.Second, overrides.TimeBudget)
	require.Equal(t, 3*time.Second, overrides.PlanTimeout)
	require.Equal(t, 4*time.Second, overrides.ToolTimeout)
	require.Equal(t, map[tools.Ident]time.Duration{"svc.read": 5 * time.Second}, overrides.PerToolTimeout)
	require.Equal(t, 6*time.Second, overrides.FinalizerGrace)
	require.True(t, overrides.InterruptsAllowed)
}

func TestPolicyOverridesUnmarshalJSONPreservesNilPerToolTimeout(t *testing.T) {
	t.Parallel()

	var overrides PolicyOverrides
	require.NoError(t, json.Unmarshal([]byte(`{}`), &overrides))
	require.Nil(t, overrides.PerToolTimeout)
}

func TestPolicyOverridesTerminalPolicyJSONRoundTrip(t *testing.T) {
	t.Parallel()

	want := PolicyOverrides{
		CompletionTool: "reports.persist",
		LimitTerminalPlans: &LimitTerminalPlans{
			TimeBudget:  LimitTerminalCall{Name: "reports.limit", Payload: []byte(`{"reason":"time"}`)},
			ToolCallCap: LimitTerminalCall{Name: "reports.limit", Payload: []byte(`{"reason":"tools"}`)},
			RecoveryCap: LimitTerminalCall{Name: "reports.limit", Payload: []byte(`{"reason":"recovery"}`)},
		},
	}
	data, err := json.Marshal(want)
	require.NoError(t, err)
	var got PolicyOverrides
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, want.CompletionTool, got.CompletionTool)
	require.Equal(t, want.LimitTerminalPlans, got.LimitTerminalPlans)
}

func TestModelRecoveryJSONRoundTrip(t *testing.T) {
	t.Parallel()

	want := ModelRecovery{
		Kind:          model.OutputValidationToolIdentity,
		Fingerprint:   [32]byte{1, 2, 3},
		ByteCount:     42,
		Usage:         model.TokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
		Attempt:       4,
		CorrectionRef: "sha256:correction",
		DisableTools:  false,
		ToolCatalog:   []tools.Ident{"svc.read", "runtime.tool_unavailable"},
	}
	data, err := json.Marshal(want)
	require.NoError(t, err)
	require.NotContains(t, string(data), "[1,2,3")

	var got ModelRecovery
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, want, got)
}
