package eval_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/agenttest"
	"github.com/CaliLuke/loom-mcp/v2/eval"
	"github.com/CaliLuke/loom-mcp/v2/expect"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func emptyFactory() eval.HarnessFunc {
	return eval.NewHarnessFunc(func(context.Context, *runtime.Runtime) error {
		return nil
	}, nil)
}

func TestRunnerMatrix(t *testing.T) {
	cfg := planConfig()
	cfg.Harness = emptyFactory()
	cfg.Repeats = 3
	keys, err := eval.Plan(cfg)
	require.NoError(t, err)
	report, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	require.Len(t, report.Attempts, 24)
	for i, a := range report.Attempts {
		assert.Equal(t, keys[i], a.AttemptKey)
		assert.Equal(t, i, a.Index)
		assert.NotNil(t, a.Outcome)
		require.NoError(t, a.HarnessErr)
	}
	assert.Equal(t, 3, report.Repeats)
	assert.Equal(t, 1, report.Concurrency)
	require.Len(t, report.Scenarios, 2)
	assert.Equal(t, []string{"a", "b"}, report.Scenarios[0].Items)
	assert.Equal(t, cfg.Scenarios[0].Items.Hash(), report.Scenarios[0].DatasetHash)
}

func TestRunnerConcurrency(t *testing.T) {
	cfg := planConfig()
	cfg.Harness = emptyFactory()
	cfg.Variants = nil
	cfg.Scenarios = cfg.Scenarios[:1]
	cfg.Repeats = 6
	cfg.Concurrency = 4
	var active, peak atomic.Int32
	barrier := make(chan struct{})
	var release sync.Once
	cfg.Scenarios[0].Drive = func(ctx context.Context, _ *agenttest.Harness, _ eval.Item) (*agenttest.SessionResult, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		if n == 4 {
			release.Do(func() {
				close(barrier)
			})
		}
		select {
		case <-barrier:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return syntheticSession(), nil
	}
	var callbackActive atomic.Int32
	counts := make(map[int]int)
	cfg.OnAttempt = func(a *eval.AttemptRecord) {
		assert.Equal(t, int32(1), callbackActive.Add(1))
		counts[a.Index]++
		callbackActive.Add(-1)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	r, err := eval.Run(ctx, cfg)
	require.NoError(t, err)
	assert.Equal(t, int32(4), peak.Load())
	require.Len(t, r.Attempts, 12)
	for i, a := range r.Attempts {
		assert.Equal(t, i, a.Index)
		assert.Equal(t, 1, counts[i])
	}
}

func TestRunnerScheduling(t *testing.T) {
	cfg := planConfig()
	cfg.Harness = emptyFactory()
	cfg.Variants = nil
	s := cfg.Scenarios[0]
	s.Items = eval.Items(eval.Item{ID: "a", Input: "hi"})
	s.ID = "short"
	s.Timeout = time.Second
	long := s
	long.ID = "long"
	long.Timeout = 5 * time.Second
	unlimited := s
	unlimited.ID = "unlimited"
	unlimited.Timeout = 0
	cfg.Scenarios = []eval.Scenario{s, long, unlimited}
	var order []string
	cfg.OnAttempt = func(a *eval.AttemptRecord) {
		order = append(order, a.Scenario)
	}
	r, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	assert.Equal(t, []string{"unlimited", "long", "short"}, order)
	assert.Equal(t, "short", r.Attempts[0].Scenario)
}

func TestRunnerCancellation(t *testing.T) {
	cfg := planConfig()
	cfg.Harness = emptyFactory()
	cfg.Variants = nil
	cfg.Scenarios = cfg.Scenarios[:1]
	cfg.Scenarios[0].Items = eval.Items(eval.Item{ID: "one", Input: "one"}, eval.Item{ID: "two", Input: "two"}, eval.Item{ID: "three", Input: "three"}, eval.Item{ID: "four", Input: "four"})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg.Scenarios[0].Drive = func(_ context.Context, _ *agenttest.Harness, item eval.Item) (*agenttest.SessionResult, error) {
		if item.ID == "two" {
			cancel()
		}
		return syntheticSession(), nil
	}
	var observed int
	cfg.OnAttempt = func(*eval.AttemptRecord) {
		observed++
	}
	r, err := eval.Run(ctx, cfg)
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, r.Attempts, 4)
	assert.Equal(t, eval.VerdictPending, r.Attempts[0].Verdict())
	assert.Equal(t, eval.VerdictError, r.Attempts[1].Verdict())
	require.ErrorIs(t, r.Attempts[1].Outcome.Err, context.Canceled)
	assert.Equal(t, eval.StopCanceled, r.Attempts[2].NotRun)
	assert.Nil(t, r.Attempts[2].Outcome)
	assert.Equal(t, 4, observed)
}

func TestRunnerLifecycleFailures(t *testing.T) {
	sentinel := errors.New("factory failure")
	for _, tc := range []struct {
		name    string
		factory eval.HarnessFunc
		message string
	}{
		{"error", func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
			return nil, sentinel
		}, "factory failure"},
		{"panic", func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
			panic("factory panic")
		}, "goroutine"},
		{"nil", func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
			return nil, nil
		}, "nil harness"},
		{"close", eval.NewHarnessFunc(func(context.Context, *runtime.Runtime) error {
			return nil
		}, nil, agenttest.ScriptedModel("unused", agenttest.Respond(agenttest.Text("unused")))), "unconsumed turn"},
		{"setup", eval.NewHarnessFunc(func(context.Context, *runtime.Runtime) error {
			return nil
		}, func(*agenttest.Harness, eval.Variant) error {
			return sentinel
		}), "factory failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := planConfig()
			cfg.Harness = tc.factory
			cfg.Select.Items = []string{"one/a"}
			cfg.Variants = nil
			r, err := eval.Run(t.Context(), cfg)
			require.NoError(t, err)
			require.ErrorContains(t, r.Attempts[0].HarnessErr, tc.message)
			assert.Equal(t, eval.VerdictError, r.Attempts[0].Verdict())
			assert.NotNil(t, r.Attempts[0].Outcome)
		})
	}
}

func TestRunnerReusedHarness(t *testing.T) {
	h, err := agenttest.NewHarness(t.Context(), func(context.Context, *runtime.Runtime) error {
		return nil
	})
	require.NoError(t, err)
	cfg := planConfig()
	cfg.Variants = nil
	cfg.Scenarios = cfg.Scenarios[:1]
	cfg.Harness = func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
		return h, nil
	}
	cfg.Scenarios[0].Drive = func(ctx context.Context, h *agenttest.Harness, _ eval.Item) (*agenttest.SessionResult, error) {
		h.Passthrough()
		return syntheticSession(), nil
	}
	r, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	assert.NoError(t, r.Attempts[0].HarnessErr)
	require.ErrorContains(t, r.Attempts[1].HarnessErr, "reused")
	_, err = h.RunSession(t.Context(), "root")
	require.ErrorContains(t, err, "closed")
}

func TestRunnerLiveGate(t *testing.T) {
	t.Setenv("LOOM_LIVE_MODEL", "")
	var registered int
	cfg := planConfig()
	cfg.Variants = []eval.Variant{{ID: "live", Options: []agenttest.Option{agenttest.LiveModel("m")}}}
	cfg.Harness = eval.NewHarnessFunc(func(context.Context, *runtime.Runtime) error {
		registered++
		return nil
	}, nil)
	r, err := eval.Run(t.Context(), cfg)
	require.ErrorIs(t, err, agenttest.ErrLiveModelDisabled)
	assert.Zero(t, registered)
	require.ErrorIs(t, r.Attempts[0].HarnessErr, agenttest.ErrLiveModelDisabled)
	for _, a := range r.Attempts[1:] {
		assert.Equal(t, eval.StopLiveModelDisabled, a.NotRun)
	}
}

func TestRunnerInvalidConfig(t *testing.T) {
	r, err := eval.Run(t.Context(), eval.Config{})
	assert.Nil(t, r)
	require.Error(t, err)
}

func TestRunnerClosesHarnessOnFactoryError(t *testing.T) {
	sentinel := errors.New("factory rejected")
	var built *agenttest.Harness
	cfg := planConfig()
	cfg.Variants = nil
	cfg.Select.Items = []string{"one/a"}
	cfg.Harness = func(ctx context.Context, _ eval.Variant, opts ...agenttest.Option) (*agenttest.Harness, error) {
		h, err := agenttest.NewHarness(ctx, func(context.Context, *runtime.Runtime) error {
			return nil
		}, opts...)
		if err != nil {
			return nil, err
		}
		built = h
		return h, sentinel
	}
	r, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	require.ErrorIs(t, r.Attempts[0].HarnessErr, sentinel)
	_, err = built.RunSession(t.Context(), "root")
	require.ErrorContains(t, err, "closed")
}

func TestRunnerClosesHarnessOnSetupPanic(t *testing.T) {
	var built *agenttest.Harness
	cfg := planConfig()
	cfg.Variants = nil
	cfg.Select.Items = []string{"one/a"}
	cfg.Harness = eval.NewHarnessFunc(func(context.Context, *runtime.Runtime) error {
		return nil
	}, func(h *agenttest.Harness, _ eval.Variant) error {
		built = h
		panic("setup panic")
	})
	r, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	require.ErrorContains(t, r.Attempts[0].HarnessErr, "setup panic")
	require.ErrorContains(t, r.Attempts[0].HarnessErr, "goroutine")
	_, err = built.RunSession(t.Context(), "root")
	require.ErrorContains(t, err, "closed")
}

func TestRunnerCanceledBeforeStart(t *testing.T) {
	cfg := planConfig()
	var built int
	cfg.Harness = func(context.Context, eval.Variant, ...agenttest.Option) (*agenttest.Harness, error) {
		built++
		return nil, errors.New("unexpected start")
	}
	cause := errors.New("caller stopped")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	r, err := eval.Run(ctx, cfg)
	require.ErrorIs(t, err, cause)
	assert.Zero(t, built)
	for _, a := range r.Attempts {
		assert.Equal(t, eval.StopCanceled, a.NotRun)
		assert.Nil(t, a.Outcome)
	}
}

func TestRunnerUsageIncludesEveryCompletion(t *testing.T) {
	session := syntheticSession()
	session.Runs[0].Record = &runrecord.RunRecord{Runs: []runrecord.Run{
		{Calls: []runrecord.Call{
			{Class: runrecord.Adopted, Completion: &callrecord.Completion{Usage: model.TokenUsage{InputTokens: 60, OutputTokens: 40, TotalTokens: 100, Model: "attributed"}}},
			{Class: runrecord.Abandoned, Completion: &callrecord.Completion{Usage: model.TokenUsage{InputTokens: 5, OutputTokens: 6, CacheReadTokens: 7, CacheWriteTokens: 8}}},
			{Class: runrecord.Incomplete},
		}},
		{Calls: []runrecord.Call{{Completion: &callrecord.Completion{Usage: model.TokenUsage{InputTokens: 10, TotalTokens: 10}}}}},
	}}
	cfg := eval.Config{Harness: emptyFactory(), Scenarios: []eval.Scenario{customScenario(session)}}
	r, err := eval.Run(t.Context(), cfg)
	require.NoError(t, err)
	assert.Equal(t, 136, r.Tokens)
	assert.Equal(t, model.TokenUsage{InputTokens: 75, OutputTokens: 46, CacheReadTokens: 7, CacheWriteTokens: 8, TotalTokens: 110}, r.Attempts[0].Usage)
}

func TestRunnerDocumentation(t *testing.T) {
	ctx := t.Context()
	register := agenttest.Register(func(context.Context, *runtime.Runtime) error {
		return nil
	})
	scenario := customScenario(syntheticSession())
	scenario.Claims = nil
	scenario.Expect = []expect.Expectation{expect.MaxModelCalls(0)}
	scenarios := []eval.Scenario{scenario}
	// snippet:start eval-runner
	report, err := eval.Run(ctx, eval.Config{
		Harness:   eval.NewHarnessFunc(register, nil),
		Scenarios: scenarios,
		Variants: []eval.Variant{
			{ID: "baseline", Labels: map[string]string{"prompt": "baseline"}},
			{ID: "candidate", Policy: &api.PolicyOverrides{PerTurnMaxToolCalls: 1}},
		},
		Repeats:     5,
		Concurrency: 2,
		MaxTokens:   10000,
	})
	// snippet:end eval-runner
	require.NoError(t, err)
	require.Len(t, report.Attempts, 10)
	assert.Equal(t, 5, report.Groups()[0].Passed)
}
