package middleware

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type countingClient struct {
	fakeClient
	count      model.TokenCount
	countErr   error
	countCalls int
}

type streamResult struct {
	chunk model.Chunk
	err   error
}

type scriptedStreamer struct {
	results    []streamResult
	closeErr   error
	recvCalls  int
	closeCalls int
}

func (c *countingClient) CountTokens(context.Context, *model.Request) (model.TokenCount, error) {
	c.countCalls++
	return c.count, c.countErr
}

func (s *scriptedStreamer) Recv() (model.Chunk, error) {
	s.recvCalls++
	if len(s.results) == 0 {
		return nil, io.EOF
	}
	result := s.results[0]
	s.results = s.results[1:]
	return result.chunk, result.err
}

func (s *scriptedStreamer) Close() error {
	s.closeCalls++
	return s.closeErr
}

func (s *scriptedStreamer) Response() *model.Response {
	return nil
}

func (s *scriptedStreamer) Finalize(primaryErr error) error {
	return errors.Join(primaryErr, s.Close())
}

func TestAdaptiveRateLimiterStreamSetupContract(t *testing.T) {
	cases := []struct {
		name       string
		streamErr  error
		wantChange func(t *testing.T, before, after float64)
	}{
		{
			name: "success_does_not_probe",
			wantChange: func(t *testing.T, before, after float64) {
				assert.InDelta(t, before, after, 0)
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
			client := &fakeClient{
				stream:    &scriptedStreamer{},
				streamErr: tt.streamErr,
			}
			wrapped := limiter.Middleware()(client)
			before := limiter.currentTPM

			stream, err := wrapped.Stream(context.Background(), &model.Request{})
			if tt.streamErr == nil {
				require.NoError(t, err)
				require.NotNil(t, stream)
			} else {
				require.ErrorIs(t, err, tt.streamErr)
				assert.Nil(t, stream)
			}
			assert.Equal(t, 1, client.streamCalls)
			tt.wantChange(t, before, limiter.currentTPM)
		})
	}
}

func TestAdaptiveRateLimiterStreamObservesTerminalRecvOutcomeOnce(t *testing.T) {
	terminalFailure := errors.New("provider stream failed")
	tests := []struct {
		name        string
		terminalErr error
		wantChange  func(t *testing.T, before, after float64)
	}{
		{
			name:        "EOF probes",
			terminalErr: io.EOF,
			wantChange: func(t *testing.T, before, after float64) {
				assert.Greater(t, after, before)
			},
		},
		{
			name:        "receive-time rate limit backs off",
			terminalErr: model.ErrRateLimited,
			wantChange: func(t *testing.T, before, after float64) {
				assert.Less(t, after, before)
			},
		},
		{
			name:        "ordinary receive error does not adjust",
			terminalErr: terminalFailure,
			wantChange: func(t *testing.T, before, after float64) {
				assert.InDelta(t, before, after, 0)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := newAdaptiveRateLimiter(60000, 120000)
			wantChunk := model.TextChunk{
				Message: model.Message{
					Role:  model.ConversationRoleAssistant,
					Parts: []model.Part{model.TextPart{Text: "partial"}},
				},
			}
			providerStream := &scriptedStreamer{results: []streamResult{
				{chunk: wantChunk},
				{err: tt.terminalErr},
			}}
			wrapped := limiter.Middleware()(&fakeClient{stream: providerStream})
			before := limiter.currentTPM

			stream, err := wrapped.Stream(context.Background(), &model.Request{})
			require.NoError(t, err)
			assert.InDelta(t, before, limiter.currentTPM, 0, "setup must not adjust capacity")

			chunk, err := stream.Recv()
			require.NoError(t, err)
			assert.Equal(t, wantChunk, chunk)
			assert.InDelta(t, before, limiter.currentTPM, 0, "non-terminal chunks must not adjust capacity")

			_, err = stream.Recv()
			assert.Same(t, tt.terminalErr, err)
			assert.InDelta(t, before, limiter.currentTPM, 0,
				"terminal receive must not adjust capacity before finalization")
			primaryErr := tt.terminalErr
			//nolint:errorlint // Only the literal EOF test case represents success.
			if tt.terminalErr == io.EOF {
				primaryErr = nil
			}
			finalizeErr := stream.Finalize(primaryErr)
			if primaryErr == nil {
				require.NoError(t, finalizeErr)
			} else {
				require.ErrorIs(t, finalizeErr, primaryErr)
			}
			tt.wantChange(t, before, limiter.currentTPM)
			afterTerminal := limiter.currentTPM

			_ = stream.Finalize(errors.New("ignored second primary"))
			assert.InDelta(t, afterTerminal, limiter.currentTPM, 0,
				"repeated finalization must not adjust capacity twice")
			assert.Equal(t, 2, providerStream.recvCalls)
		})
	}
}

func TestAdaptiveRateLimiterStreamDelegatesClose(t *testing.T) {
	closeErr := errors.New("provider close failed")
	providerStream := &scriptedStreamer{
		closeErr: closeErr,
	}
	limiter := newAdaptiveRateLimiter(60000, 120000)
	wrapped := limiter.Middleware()(&fakeClient{stream: providerStream})
	before := limiter.currentTPM

	stream, err := wrapped.Stream(context.Background(), &model.Request{})
	require.NoError(t, err)
	err = stream.Close()
	assert.Same(t, closeErr, err)
	assert.Equal(t, 1, providerStream.closeCalls)
	assert.InDelta(t, before, limiter.currentTPM, 0,
		"close without a terminal receive must not adjust capacity")
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
	_, ok = unsupported.(model.TokenCounter)
	require.False(t, ok, "middleware must preserve absence of optional token-counting capability")
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
