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

func TestRestrictRetryHintCannotBroadenAllowedTools(t *testing.T) {
	for _, tc := range []struct {
		name          string
		options       basic.Options
		requested     []tools.Ident
		target        tools.Ident
		want          []tools.Ident
		wantRemaining int
	}{
		{name: "blocked ID", options: basic.Options{BlockTools: []string{"beta"}}, target: "beta", wantRemaining: 5},
		{name: "blocked tag", options: basic.Options{BlockTags: []string{"sensitive"}}, target: "beta", wantRemaining: 5},
		{name: "outside allowed IDs", options: basic.Options{AllowTools: []string{"alpha"}}, target: "beta", wantRemaining: 5},
		{name: "outside allowed tags", options: basic.Options{AllowTags: []string{"read"}}, target: "beta", wantRemaining: 5},
		{name: "outside requested candidates", requested: []tools.Ident{"alpha"}, target: "beta", wantRemaining: 5},
		{name: "unknown target", target: "unknown", wantRemaining: 5},
		{name: "allowed target", target: "alpha", want: []tools.Ident{"alpha"}, wantRemaining: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, err := basic.New(tc.options)
			require.NoError(t, err)
			decision, err := engine.Decide(context.Background(), policy.Input{
				Tools:         []policy.ToolMetadata{{ID: "alpha", Tags: []string{"read"}}, {ID: "beta", Tags: []string{"sensitive"}}},
				Requested:     tc.requested,
				RetryHint:     &policy.RetryHint{Tool: tc.target, RestrictToTool: true},
				RemainingCaps: policy.CapsState{MaxToolCalls: 5, RemainingToolCalls: 5},
			})
			require.NoError(t, err)
			assert.Equal(t, tc.want, decision.AllowedTools)
			assert.Equal(t, tc.wantRemaining, decision.Caps.RemainingToolCalls)
		})
	}
}
