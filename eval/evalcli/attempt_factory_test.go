package evalcli_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
)

func TestRunAttemptHarness(t *testing.T) {
	cfg := config(t)
	legacy := cfg.Harness
	cfg.Harness = nil
	var seen []eval.AttemptKey
	cfg.AttemptHarness = func(ctx context.Context, info eval.AttemptInfo, opts ...agenttest.Option) (*agenttest.Harness, error) {
		seen = append(seen, info.Key)
		return legacy(ctx, info.Variant, opts...)
	}
	code, _, diagnostic := invoke(t.Context(), cfg, "run", "--item", "pass", "--repeat", "2")
	require.Equal(t, 0, code, diagnostic)
	assert.Equal(t, []eval.AttemptKey{{Variant: "default", Scenario: "s", Item: "pass", Repeat: 1}, {Variant: "default", Scenario: "s", Item: "pass", Repeat: 2}}, seen)
	store, headers := experiments(t, cfg.StoreDir)
	require.Len(t, headers, 1)
	experiment, err := store.Load(headers[0].ID)
	require.NoError(t, err)
	require.Len(t, experiment.Attempts, 2)
	assert.Equal(t, seen[0], experiment.Attempts[0].AttemptKey)
	assert.Equal(t, seen[1], experiment.Attempts[1].AttemptKey)
	cfg.Harness = legacy
	code, _, diagnostic = invoke(t.Context(), cfg, "run")
	assert.Equal(t, 2, code)
	assert.Contains(t, diagnostic, "Harness")
}
