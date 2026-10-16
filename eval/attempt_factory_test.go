package eval_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func TestAttemptHarnessSelectionAndIdentity(t *testing.T) {
	cfg := planConfig()
	cfg.Harness = nil
	cfg.Repeats = 3
	cfg.Select = eval.Selection{Keys: []eval.AttemptKey{{Variant: "v2", Scenario: "two", Item: "b", Repeat: 2}}}
	var seen []eval.AttemptInfo
	cfg.AttemptHarness = func(ctx context.Context, info eval.AttemptInfo, opts ...agenttest.Option) (*agenttest.Harness, error) {
		seen = append(seen, info)
		return emptyFactory()(ctx, info.Variant, opts...)
	}
	keys, err := eval.Plan(cfg)
	require.NoError(t, err)
	report, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	require.Len(t, seen, 1)
	assert.Equal(t, keys[0], seen[0].Key)
	assert.Equal(t, "b", seen[0].Item.ID)
	assert.Equal(t, "v2", seen[0].Variant.ID)
	assert.Equal(t, keys[0], report.Attempts[0].AttemptKey)
	assert.NoError(t, report.Attempts[0].HarnessErr)
}

func TestAttemptHarnessOwnsConcurrentMetadata(t *testing.T) {
	item := eval.Item{ID: "item", Turns: []string{"original"}, Data: []byte(`{"n":1}`), ExpectedTools: &expect.ExpectedTools{Tools: []expect.ExpectedTool{{Name: "tool", Args: []byte(`{"n":1}`)}}}}
	scenario := customScenario(syntheticSession())
	scenario.Items = eval.Items(item)
	scenario.Drive = func(_ context.Context, _ *agenttest.Harness, item eval.Item) (*agenttest.SessionResult, error) {
		assert.Equal(t, "item", item.ID)
		assert.Equal(t, []string{"original"}, item.Turns)
		assert.JSONEq(t, `{"n":1}`, string(item.Data))
		assert.JSONEq(t, `{"n":1}`, string(item.ExpectedTools.Tools[0].Args))
		return syntheticSession(), nil
	}
	cfg := eval.Config{Scenarios: []eval.Scenario{scenario}, Repeats: 12, Concurrency: 4, Variants: []eval.Variant{{ID: "variant", Labels: map[string]string{"key": "original"}, Policy: &api.PolicyOverrides{AllowedTags: []string{"original"}, PerToolTimeout: map[tools.Ident]time.Duration{"tool": time.Second}}, Options: []agenttest.Option{agenttest.WithRunTimeout(time.Minute)}}}}
	var built atomic.Int32
	cfg.AttemptHarness = func(ctx context.Context, info eval.AttemptInfo, opts ...agenttest.Option) (*agenttest.Harness, error) {
		built.Add(1)
		assert.Equal(t, []string{"original"}, info.Item.Turns)
		assert.JSONEq(t, `{"n":1}`, string(info.Item.Data))
		assert.JSONEq(t, `{"n":1}`, string(info.Item.ExpectedTools.Tools[0].Args))
		assert.Equal(t, "original", info.Variant.Labels["key"])
		assert.Equal(t, []string{"original"}, info.Variant.Policy.AllowedTags)
		assert.Equal(t, time.Second, info.Variant.Policy.PerToolTimeout["tool"])
		assert.NotNil(t, info.Variant.Options[0])
		info.Item.ID = "mutated"
		info.Item.Turns[0] = "mutated"
		info.Item.Data[5] = '2'
		info.Item.ExpectedTools.Tools[0].Args[5] = '2'
		info.Variant.Labels["key"] = "mutated"
		info.Variant.Policy.AllowedTags[0] = "mutated"
		info.Variant.Policy.PerToolTimeout["tool"] = 2 * time.Second
		info.Variant.Options[0] = nil
		return emptyFactory()(ctx, info.Variant, opts...)
	}
	report, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	assert.EqualValues(t, 12, built.Load())
	assert.Equal(t, "original", cfg.Variants[0].Labels["key"])
	assert.Equal(t, "original", report.Variants[0].Labels["key"])
	assert.Equal(t, []string{"original"}, cfg.Scenarios[0].Items.Items()[0].Turns)
	for _, attempt := range report.Attempts {
		assert.NoError(t, attempt.HarnessErr)
		assert.NoError(t, attempt.Outcome.Err)
		assert.Equal(t, "item", attempt.Item)
	}
}

func TestAttemptHarnessConfiguration(t *testing.T) {
	for _, both := range []bool{false, true} {
		t.Run(map[bool]string{false: "neither", true: "both"}[both], func(t *testing.T) {
			cfg := planConfig()
			cfg.Harness = nil
			if both {
				cfg.Harness = emptyFactory()
				cfg.AttemptHarness = func(ctx context.Context, info eval.AttemptInfo, opts ...agenttest.Option) (*agenttest.Harness, error) {
					return emptyFactory()(ctx, info.Variant, opts...)
				}
			}
			_, err := eval.Plan(cfg)
			require.ErrorContains(t, err, "Harness")
		})
	}
}

func TestAttemptHarnessCanceledBeforeStart(t *testing.T) {
	cfg := planConfig()
	cfg.Harness = nil
	var built atomic.Int32
	cfg.AttemptHarness = func(context.Context, eval.AttemptInfo, ...agenttest.Option) (*agenttest.Harness, error) {
		built.Add(1)
		return nil, errors.New("unexpected start")
	}
	cause := errors.New("caller stopped")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	report, err := eval.Run(ctx, cfg)
	require.ErrorIs(t, err, cause)
	assert.Zero(t, built.Load())
	for _, attempt := range report.Attempts {
		assert.Equal(t, eval.StopCanceled, attempt.NotRun)
	}
}

func TestAttemptHarnessFailureAndReuse(t *testing.T) {
	for _, path := range []string{"error", "panic", "reuse"} {
		t.Run(path, func(t *testing.T) {
			cfg := eval.Config{Scenarios: []eval.Scenario{customScenario(syntheticSession())}, Repeats: 2}
			var first *agenttest.Harness
			cfg.AttemptHarness = func(ctx context.Context, info eval.AttemptInfo, opts ...agenttest.Option) (*agenttest.Harness, error) {
				switch path {
				case "error":
					return nil, errors.New("factory rejected")
				case "panic":
					panic("factory panicked")
				}
				if first != nil {
					return first, nil
				}
				h, err := emptyFactory()(ctx, info.Variant, opts...)
				first = h
				return h, err
			}
			report, err := eval.Run(t.Context(), cfg)
			require.NoError(t, err)
			last := report.Attempts[1]
			require.Error(t, last.HarnessErr)
			assert.Contains(t, last.HarnessErr.Error(), map[string]string{"error": "factory rejected", "panic": "factory panicked", "reuse": "reused a harness"}[path])
		})
	}
}
