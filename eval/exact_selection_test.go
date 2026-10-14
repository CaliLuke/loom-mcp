package eval_test

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK19cExactSelectionRunsAndPersistsSparseRepeats(t *testing.T) {
	cfg := planConfig()
	cfg.Repeats = 3
	wanted := []eval.AttemptKey{
		{Variant: "v1", Scenario: "one", Item: "b", Repeat: 2},
		{Variant: "v2", Scenario: "two", Item: "a", Repeat: 3},
	}
	cfg.Select.Keys = []eval.AttemptKey{wanted[1], wanted[0]}
	keys, err := eval.Plan(cfg)
	require.NoError(t, err)
	require.Equal(t, wanted, keys, "declaration order, not selector order or Cartesian expansion")
	var calls atomic.Int32
	factory := emptyFactory()
	cfg.Harness = func(ctx context.Context, v eval.Variant, opts ...agenttest.Option) (*agenttest.Harness, error) {
		calls.Add(1)
		return factory(ctx, v, opts...)
	}
	store, err := eval.Open(filepath.Join(t.TempDir(), "store"))
	require.NoError(t, err)
	writer, err := store.Begin(t.Context(), cfg, eval.ExperimentMeta{Identity: &eval.Identity{}})
	require.NoError(t, err)
	cfg.OnAttempt = func(a *eval.AttemptRecord) {
		assert.NoError(t, writer.Append(a))
	}
	report, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	require.NoError(t, writer.Finish(report, nil))
	assert.Equal(t, int32(2), calls.Load())
	require.Len(t, report.Attempts, 2)
	assert.Equal(t, wanted[0], report.Attempts[0].AttemptKey)
	assert.Equal(t, wanted[1], report.Attempts[1].AttemptKey)
	assert.NotNil(t, report.Attempts[0].Outcome)
	assert.NotNil(t, report.Attempts[1].Outcome)
	loaded, err := store.Load(writer.ID())
	require.NoError(t, err)
	assert.True(t, loaded.Complete())
	assert.Equal(t, cfg.Select.Keys, report.Selection.Keys)
	assert.Equal(t, cfg.Select.Keys, loaded.Header.Config.Selection.Keys)
	cfg.Select.Keys[0].Repeat = 1
	assert.Equal(t, 3, report.Selection.Keys[0].Repeat, "report owns its selection")
	report.Selection.Keys[0].Repeat = 2
	again, err := store.Load(writer.ID())
	require.NoError(t, err)
	assert.Equal(t, 3, again.Header.Config.Selection.Keys[0].Repeat, "persisted header owns its selection")
}

func TestK19cExactSelectionRejectsBeforeWork(t *testing.T) {
	valid := eval.AttemptKey{Variant: "v1", Scenario: "one", Item: "a", Repeat: 2}
	for _, tc := range []struct {
		name      string
		selection eval.Selection
	}{
		{"empty", eval.Selection{Keys: []eval.AttemptKey{}}},
		{"duplicate", eval.Selection{Keys: []eval.AttemptKey{valid, valid}}},
		{"zero key", eval.Selection{Keys: []eval.AttemptKey{{}}}},
		{"unknown variant", eval.Selection{Keys: []eval.AttemptKey{{Variant: "missing", Scenario: "one", Item: "a", Repeat: 2}}}},
		{"unknown scenario", eval.Selection{Keys: []eval.AttemptKey{{Variant: "v1", Scenario: "missing", Item: "a", Repeat: 2}}}},
		{"unknown item", eval.Selection{Keys: []eval.AttemptKey{{Variant: "v1", Scenario: "one", Item: "missing", Repeat: 2}}}},
		{"repeat zero", eval.Selection{Keys: []eval.AttemptKey{{Variant: "v1", Scenario: "one", Item: "a"}}}},
		{"repeat beyond matrix", eval.Selection{Keys: []eval.AttemptKey{{Variant: "v1", Scenario: "one", Item: "a", Repeat: 4}}}},
		{"mixed variant", eval.Selection{Keys: []eval.AttemptKey{valid}, Variants: []string{"v1"}}},
		{"mixed scenario", eval.Selection{Keys: []eval.AttemptKey{valid}, Scenarios: []string{"one"}}},
		{"mixed tag", eval.Selection{Keys: []eval.AttemptKey{valid}, Tags: []string{"red"}}},
		{"mixed item", eval.Selection{Keys: []eval.AttemptKey{valid}, Items: []string{"a"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := planConfig()
			cfg.Repeats = 3
			cfg.Select = tc.selection
			var calls atomic.Int32
			cfg.Harness = func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
				calls.Add(1)
				return nil, nil
			}
			_, err := eval.Plan(cfg)
			require.Error(t, err)
			_, err = eval.Run(t.Context(), cfg)
			require.Error(t, err)
			assert.Zero(t, calls.Load())
			store, err := eval.Open(t.TempDir())
			require.NoError(t, err)
			_, err = store.Begin(t.Context(), cfg, eval.ExperimentMeta{Identity: &eval.Identity{}})
			require.Error(t, err)
			entries, err := store.List()
			require.NoError(t, err)
			assert.Empty(t, entries)
		})
	}
}
