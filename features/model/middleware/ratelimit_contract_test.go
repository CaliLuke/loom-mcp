package middleware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type countingClient struct {
	fakeClient
	count      model.TokenCount
	countErr   error
	countCalls int
}

type concurrentCountingClient struct {
	countCalls    atomic.Int32
	completeCalls atomic.Int32
	countErr      error
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

func (c *concurrentCountingClient) Complete(context.Context, *model.Request) (*model.Response, error) {
	c.completeCalls.Add(1)
	return nil, nil
}

func (*concurrentCountingClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	return nil, nil
}

func (c *concurrentCountingClient) CountTokens(context.Context, *model.Request) (model.TokenCount, error) {
	c.countCalls.Add(1)
	return model.TokenCount{InputTokens: 25, Exact: true}, c.countErr
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

func TestAdaptiveRateLimiterKeepsEstimatedInputOnlyAdmission(t *testing.T) {
	native := &countingClient{
		count: model.TokenCount{
			InputTokens: 100,
			Exact:       true,
		},
	}
	limiter := NewAdaptiveRateLimiter(t.Context(), nil, "", 1000, 1000)
	wrapped := limiter.Middleware()(native)

	_, err := wrapped.Complete(t.Context(), &model.Request{MaxTokens: 50})

	require.NoError(t, err)
	assert.Zero(t, native.countCalls)
	assert.Equal(t, 1, native.completeCalls)
}

func TestOutputReservationAdaptiveRateLimiterChargesExactInputAndOutput(t *testing.T) {
	native := &countingClient{
		count: model.TokenCount{
			InputTokens: 100,
			Exact:       true,
		},
	}
	limiter := NewOutputReservationAdaptiveRateLimiter(
		t.Context(),
		nil,
		"",
		1000,
		1000,
	)
	limiter.limiter = rate.NewLimiter(0, 1000)
	wrapped := limiter.Middleware()(native)

	_, err := wrapped.Complete(t.Context(), &model.Request{MaxTokens: 50})

	require.NoError(t, err)
	assert.Equal(t, 1, native.countCalls)
	assert.Equal(t, 1, native.completeCalls)
	assert.InDelta(t, 850, limiter.limiter.Tokens(), 0.001)
}

func TestOutputReservationAdaptiveRateLimiterReservesExactlyUnderConcurrency(t *testing.T) {
	const calls = 10

	native := &concurrentCountingClient{}
	limiter := NewOutputReservationAdaptiveRateLimiter(
		t.Context(),
		nil,
		"",
		1000,
		1000,
	)
	limiter.limiter = rate.NewLimiter(0, 1000)
	wrapped := limiter.Middleware()(native)

	var wg sync.WaitGroup
	errs := make(chan error, calls)
	for range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := wrapped.Complete(t.Context(), &model.Request{MaxTokens: 75})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(calls), native.countCalls.Load())
	assert.Equal(t, int32(calls), native.completeCalls.Load())
	assert.InDelta(t, 0, limiter.limiter.Tokens(), 0.001)
}

func TestOutputReservationAdaptiveRateLimiterRejectsInvalidCosts(t *testing.T) {
	tests := []struct {
		name       string
		request    *model.Request
		count      model.TokenCount
		countErr   error
		wantError  string
		countCalls int
	}{
		{
			name:      "nil request",
			wantError: "requires positive max tokens",
		},
		{
			name:      "zero max tokens",
			request:   &model.Request{},
			wantError: "requires positive max tokens",
		},
		{
			name:      "negative max tokens",
			request:   &model.Request{MaxTokens: -1},
			wantError: "requires positive max tokens",
		},
		{
			name:       "inexact input count",
			request:    &model.Request{MaxTokens: 1},
			count:      model.TokenCount{InputTokens: 100},
			wantError:  "requires an exact provider token count",
			countCalls: 1,
		},
		{
			name:       "negative input count",
			request:    &model.Request{MaxTokens: 1},
			count:      model.TokenCount{InputTokens: -1, Exact: true},
			wantError:  "returned a negative input token count",
			countCalls: 1,
		},
		{
			name:       "integer overflow",
			request:    &model.Request{MaxTokens: 1},
			count:      model.TokenCount{InputTokens: math.MaxInt, Exact: true},
			wantError:  "token cost exceeds integer range",
			countCalls: 1,
		},
		{
			name:       "counter error",
			request:    &model.Request{MaxTokens: 1},
			countErr:   errors.New("count failed"),
			wantError:  "count failed",
			countCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			native := &countingClient{count: tt.count, countErr: tt.countErr}
			limiter := NewOutputReservationAdaptiveRateLimiter(
				t.Context(),
				nil,
				"",
				1000,
				1000,
			)
			wrapped := limiter.Middleware()(native)

			_, err := wrapped.Complete(t.Context(), tt.request)

			require.ErrorContains(t, err, tt.wantError)
			assert.Equal(t, tt.countCalls, native.countCalls)
			assert.Zero(t, native.completeCalls)
		})
	}
}

func TestOutputReservationAdaptiveRateLimiterBacksOffOnCountRateLimit(t *testing.T) {
	native := &countingClient{countErr: fmt.Errorf("count tokens: %w", model.ErrRateLimited)}
	limiter := NewOutputReservationAdaptiveRateLimiter(
		t.Context(),
		nil,
		"",
		1000,
		1000,
	)
	wrapped := limiter.Middleware()(native)

	_, err := wrapped.Complete(t.Context(), &model.Request{MaxTokens: 50})

	require.ErrorIs(t, err, model.ErrRateLimited)
	assert.InDelta(t, 500, limiter.currentTPM, 0)
	assert.Equal(t, 1, native.countCalls)
	assert.Zero(t, native.completeCalls)
}

func TestOutputReservationAdaptiveRateLimiterRequiresTokenCounter(t *testing.T) {
	native := &fakeClient{}
	limiter := NewOutputReservationAdaptiveRateLimiter(
		t.Context(),
		nil,
		"",
		1000,
		1000,
	)
	wrapped := limiter.Middleware()(native)

	stream, err := wrapped.Stream(t.Context(), &model.Request{MaxTokens: 50})

	require.Nil(t, stream)
	require.ErrorContains(t, err, "requires provider token counting")
	assert.Zero(t, native.streamCalls)
}

func TestOutputReservationAdaptiveRateLimiterPreservesMaximumBurst(t *testing.T) {
	native := &countingClient{
		count: model.TokenCount{
			InputTokens: 900,
			Exact:       true,
		},
	}
	limiter := NewOutputReservationAdaptiveRateLimiter(
		t.Context(),
		nil,
		"",
		600,
		1000,
	)
	wrapped := limiter.Middleware()(native)

	_, err := wrapped.Complete(t.Context(), &model.Request{MaxTokens: 101})

	require.ErrorIs(t, err, ErrRequestTooLarge)
	assert.Equal(t, 1000, limiter.limiter.Burst())
	assert.Zero(t, native.completeCalls)
}

func TestOutputReservationClusterKeySeparatesAccountingModes(t *testing.T) {
	assert.Empty(t, outputReservationClusterKey(""))
	assert.Equal(
		t,
		"model"+outputReservationClusterKeySuffix,
		outputReservationClusterKey("model"),
	)
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
