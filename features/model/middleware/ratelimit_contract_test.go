package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
)

type countingClient struct {
	fakeClient
	count      model.TokenCount
	countErr   error
	countCalls int
}

func (c *countingClient) CountTokens(context.Context, *model.Request) (model.TokenCount, error) {
	c.countCalls++
	return c.count, c.countErr
}

func TestAdaptiveRateLimiterStreamMatchesCompletionContract(t *testing.T) {
	cases := []struct {
		name       string
		streamErr  error
		wantChange func(t *testing.T, before, after float64)
	}{
		{
			name: "success_probes",
			wantChange: func(t *testing.T, before, after float64) {
				assert.Greater(t, after, before)
			},
		},
		{
			name:      "rate_limit_backs_off",
			streamErr: model.ErrRateLimited,
			wantChange: func(t *testing.T, before, after float64) {
				assert.Less(t, after, before)
			},
		},
		{
			name:      "ordinary_error_does_not_adjust",
			streamErr: errors.New("provider failed"),
			wantChange: func(t *testing.T, before, after float64) {
				assert.InDelta(t, before, after, 0)
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			limiter := newAdaptiveRateLimiter(60000, 120000)
			client := &fakeClient{streamErr: tt.streamErr}
			wrapped := limiter.Middleware()(client)
			before := limiter.currentTPM

			_, err := wrapped.Stream(context.Background(), &model.Request{})
			if tt.streamErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.streamErr)
			}
			assert.Equal(t, 1, client.streamCalls)
			tt.wantChange(t, before, limiter.currentTPM)
		})
	}
}

func TestAdaptiveRateLimiterPreservesTokenCounterCapability(t *testing.T) {
	want := model.TokenCount{Model: "provider-model", InputTokens: 42, Exact: true}
	native := &countingClient{count: want}
	wrapped := newAdaptiveRateLimiter(60000, 60000).Middleware()(native)
	counter, ok := wrapped.(model.TokenCounter)
	require.True(t, ok)

	got, err := counter.CountTokens(context.Background(), &model.Request{})
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, 1, native.countCalls)

	unsupported := newAdaptiveRateLimiter(60000, 60000).Middleware()(&fakeClient{})
	counter, ok = unsupported.(model.TokenCounter)
	require.True(t, ok)
	_, err = counter.CountTokens(context.Background(), &model.Request{})
	require.EqualError(t, err, "model middleware: wrapped client does not support token counting")
	assert.Nil(t, newAdaptiveRateLimiter(60000, 60000).Middleware()(nil))
}

func TestAdaptiveRateLimiterCancellationStopsBeforeProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &fakeClient{}
	wrapped := newAdaptiveRateLimiter(60000, 60000).Middleware()(client)

	_, err := wrapped.Stream(ctx, &model.Request{})
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, client.streamCalls)
}

func TestClusterLimiterUsesConfiguredBoundsAndEffectiveDefaults(t *testing.T) {
	t.Run("shared_value_above_max_is_clamped", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cluster := newFakeClusterMap()
		cluster.values["model"] = "1000000"

		limiter := newClusterAdaptiveRateLimiter(ctx, cluster, "model", 60000, 120000)

		assert.InDelta(t, 120000.0, limiter.currentTPM, 0)
		assert.InDelta(t, 120000.0, limiter.maxTPM, 0)
		assert.InDelta(t, 6000.0, limiter.minTPM, 0)
		assert.InDelta(t, 3000.0, limiter.recoveryRate, 0)
	})

	t.Run("zero_initial_seeds_effective_default", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cluster := newFakeClusterMap()

		limiter := newClusterAdaptiveRateLimiter(ctx, cluster, "model", 0, 0)

		value, ok := cluster.Get("model")
		require.True(t, ok)
		assert.Equal(t, "60000", value)
		assert.InDelta(t, 60000.0, limiter.currentTPM, 0)
		assert.InDelta(t, 60000.0, limiter.maxTPM, 0)
	})
}

func TestSharedTPMParsingAndProbeBoundaries(t *testing.T) {
	cluster := newFakeClusterMap()
	assert.InDelta(t, 500.0, loadSharedTPM(cluster, "missing", 500), 0)
	cluster.values["model"] = "invalid"
	assert.InDelta(t, 500.0, loadSharedTPM(cluster, "model", 500), 0)
	_, ok := parseSharedTPM(cluster, "model")
	assert.False(t, ok)
	cluster.values["model"] = "100"

	globalProbe(context.Background(), cluster, "model", 25, 120)
	value, ok := cluster.Get("model")
	require.True(t, ok)
	assert.Equal(t, "120", value)

	globalBackoff(context.Background(), cluster, "model", 80)
	value, ok = cluster.Get("model")
	require.True(t, ok)
	assert.Equal(t, "80", value)
}
