package basic_test

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/features/policy/basic"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDisableRetryHintsIsAuthoritative(t *testing.T) {
	for _, filters := range []struct {
		name       string
		blocked    []string
		candidates []policy.ToolMetadata
	}{
		{name: "without filters", candidates: []policy.ToolMetadata{{ID: "alpha"}, {ID: "beta"}}},
		{name: "with filters", blocked: []string{"gamma"}, candidates: []policy.ToolMetadata{{ID: "alpha"}, {ID: "beta"}, {ID: "gamma"}}},
	} {
		t.Run(filters.name, func(t *testing.T) {
			for _, tc := range []struct {
				name          string
				disabled      bool
				hint          policy.RetryHint
				want          []tools.Ident
				wantRemaining int
				wantLabel     bool
			}{
				{name: "disabled restriction", disabled: true, hint: policy.RetryHint{Tool: "beta", RestrictToTool: true, Reason: policy.RetryReasonInvalidArguments}, want: []tools.Ident{"alpha", "beta"}, wantRemaining: 5},
				{name: "disabled unavailable", disabled: true, hint: policy.RetryHint{Tool: "beta", Reason: policy.RetryReasonToolUnavailable}, want: []tools.Ident{"alpha", "beta"}, wantRemaining: 5},
				{name: "default restriction", hint: policy.RetryHint{Tool: "beta", RestrictToTool: true, Reason: policy.RetryReasonInvalidArguments}, want: []tools.Ident{"beta"}, wantRemaining: 1, wantLabel: true},
				{name: "default unavailable", hint: policy.RetryHint{Tool: "beta", Reason: policy.RetryReasonToolUnavailable}, want: []tools.Ident{"alpha"}, wantRemaining: 5, wantLabel: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					engine, err := basic.New(basic.Options{BlockTools: filters.blocked, DisableRetryHints: tc.disabled})
					require.NoError(t, err)
					decision, err := engine.Decide(context.Background(), policy.Input{
						Tools:         filters.candidates,
						RetryHint:     &tc.hint,
						RemainingCaps: policy.CapsState{MaxToolCalls: 5, RemainingToolCalls: 5},
					})
					require.NoError(t, err)
					assert.Equal(t, tc.want, decision.AllowedTools)
					assert.Equal(t, tc.wantRemaining, decision.Caps.RemainingToolCalls)
					_, hasHintLabel := decision.Labels["policy_hint"]
					assert.Equal(t, tc.wantLabel, hasHintLabel)
				})
			}
		})
	}
}
