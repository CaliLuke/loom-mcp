package api

import (
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func TestPlanActivityOutput_UnmarshalJSON(t *testing.T) {
	t.Run("modern transcript", func(t *testing.T) {
		const payload = `{
			"Result": null,
			"Transcript": [{
				"Role": "assistant",
				"Meta": {"trace": "abc"},
				"Parts": [
					{"Text": "hi there"},
					{"Name": "search", "Input": {"q": "golang"}},
					{"ToolUseID": "tool-call-1", "Content": {"items": 1}, "IsError": false}
				]
			}]
		}`

		var out PlanActivityOutput
		require.NoError(t, json.Unmarshal([]byte(payload), &out))
		require.Len(t, out.Transcript, 1)

		msg := out.Transcript[0]
		require.Equal(t, model.ConversationRoleAssistant, msg.Role)
		require.Equal(t, map[string]any{"trace": "abc"}, msg.Meta)
		require.Len(t, msg.Parts, 3)

		if tp, ok := msg.Parts[0].(model.TextPart); ok {
			require.Equal(t, "hi there", tp.Text)
		} else {
			t.Fatalf("unexpected part[0]: %#v", msg.Parts[0])
		}

		if tu, ok := msg.Parts[1].(model.ToolUsePart); ok {
			require.Equal(t, "search", tu.Name)
			args, ok := tu.Input.(map[string]any)
			require.True(t, ok, "expected Input to be a map")
			require.Equal(t, map[string]any{"q": "golang"}, args)
		} else {
			t.Fatalf("unexpected part[1]: %#v", msg.Parts[1])
		}

		if tr, ok := msg.Parts[2].(model.ToolResultPart); ok {
			require.Equal(t, "tool-call-1", tr.ToolUseID)
			require.False(t, tr.IsError)
			require.Equal(t, map[string]any{"items": float64(1)}, tr.Content)
		} else {
			t.Fatalf("unexpected part[2]: %#v", msg.Parts[2])
		}
	})

	t.Run("legacy args field", func(t *testing.T) {
		const legacy = `{
			"Result": null,
			"Transcript": [{
				"Role": "assistant",
				"Parts": [
					{"Name": "legacy-tool", "Args": {"q": "old"}}
				]
			}]
		}`

		var out PlanActivityOutput
		require.NoError(t, json.Unmarshal([]byte(legacy), &out))
		require.Len(t, out.Transcript, 1)

		msg := out.Transcript[0]
		require.Len(t, msg.Parts, 1)

		tu, ok := msg.Parts[0].(model.ToolUsePart)
		require.True(t, ok, "expected first part to be ToolUsePart")
		require.Equal(t, map[string]any{"q": "old"}, tu.Input)
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

func TestModelRecoveryJSONRoundTrip(t *testing.T) {
	t.Parallel()

	want := ModelRecovery{
		Kind:         model.OutputValidationToolIdentity,
		Fingerprint:  [32]byte{1, 2, 3},
		ByteCount:    42,
		Usage:        model.TokenUsage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5},
		Attempt:      4,
		Correction:   "use an advertised tool",
		DisableTools: false,
		ToolCatalog:  []tools.Ident{"svc.read", "runtime.tool_unavailable"},
	}
	data, err := json.Marshal(want)
	require.NoError(t, err)
	require.NotContains(t, string(data), "[1,2,3")

	var got ModelRecovery
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, want, got)
}
