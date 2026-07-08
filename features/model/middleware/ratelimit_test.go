package middleware

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
)

type fakeClient struct {
	completeErr error
	streamErr   error

	completeCalls int
	streamCalls   int
}

func (f *fakeClient) Complete(_ context.Context, _ *model.Request) (*model.Response, error) {
	f.completeCalls++
	return nil, f.completeErr
}

func (f *fakeClient) Stream(_ context.Context, _ *model.Request) (model.Streamer, error) {
	f.streamCalls++
	return nil, f.streamErr
}

func TestAdaptiveRateLimiter_BackoffOnRateLimited(t *testing.T) {
	t.Helper()

	limiter := newAdaptiveRateLimiter(60000, 60000)

	initialTPM := limiter.currentTPM

	client := &fakeClient{
		completeErr: model.ErrRateLimited,
	}
	wrapped := limiter.Middleware()(client)

	req := model.Request{
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: "hello"},
				},
			},
		},
		MaxTokens: 10,
	}

	_, err := wrapped.Complete(context.Background(), &req)
	if err == nil || !errors.Is(err, model.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	if limiter.currentTPM >= initialTPM {
		t.Fatalf("expected TPM to decrease, got %f (initial %f)",
			limiter.currentTPM, initialTPM)
	}
}

func TestAdaptiveRateLimiter_ProbeOnSuccess(t *testing.T) {
	t.Helper()

	limiter := newAdaptiveRateLimiter(60000, 120000)

	limiter.mu.Lock()
	initialTPM := limiter.currentTPM
	limiter.recoveryRate = 1000
	limiter.mu.Unlock()

	client := &fakeClient{}
	wrapped := limiter.Middleware()(client)

	req := model.Request{
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: "hello"},
				},
			},
		},
		MaxTokens: 10,
	}

	_, err := wrapped.Complete(context.Background(), &req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	if limiter.currentTPM <= initialTPM {
		t.Fatalf("expected TPM to increase, got %f (initial %f)",
			limiter.currentTPM, initialTPM)
	}
}

func TestAdaptiveRateLimiter_RespectsContextWhenQueued(t *testing.T) {
	t.Helper()

	limiter := newAdaptiveRateLimiter(60, 60)

	limiter.mu.Lock()
	limiter.currentTPM = 60
	// Configure an impossible limiter so any non-zero token request fails
	// immediately. This exercises the error path without relying on timing.
	limiter.limiter = rate.NewLimiter(0, 0)
	limiter.mu.Unlock()

	client := &fakeClient{}
	wrapped := limiter.Middleware()(client)

	longText := make([]byte, 600)
	for i := range longText {
		longText[i] = 'a'
	}

	req := model.Request{
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: string(longText)},
				},
			},
		},
		MaxTokens: 10,
	}

	_, err := wrapped.Complete(context.Background(), &req)
	if err == nil {
		t.Fatal("expected limiter error")
	}
	if client.completeCalls != 0 {
		t.Fatalf("expected underlying client not to be called, got %d calls",
			client.completeCalls)
	}
}

func TestAdaptiveRateLimiter_LargeRequestAdmittedAfterBackoff(t *testing.T) {
	// Regression for issue #121: repeated backoffs used to shrink the bucket
	// burst, permanently rejecting any request larger than the shrunken budget.
	// The burst is now pinned at maxTPM, so such requests wait instead of fail.
	limiter := newAdaptiveRateLimiter(6000, 6000)

	for i := 0; i < 4; i++ {
		limiter.backoff()
	}

	limiter.mu.Lock()
	shrunkenTPM := limiter.currentTPM
	limiter.mu.Unlock()
	assert.InEpsilon(t, 600.0, shrunkenTPM, 1e-9, "expected TPM shrunk to the 10%% floor")

	// ~1000 estimated tokens: 1500 chars / 3 chars-per-token + 500 overhead.
	// That is above the shrunken TPM but below the pinned burst (maxTPM).
	req := requestWithChars(1500)
	count, err := model.TokenEstimator{}.CountTokens(context.Background(), &req)
	require.NoError(t, err)
	assert.Greater(t, float64(count.InputTokens), shrunkenTPM)
	assert.LessOrEqual(t, float64(count.InputTokens), limiter.maxTPM)

	client := &fakeClient{}
	wrapped := limiter.Middleware()(client)

	_, err = wrapped.Complete(context.Background(), &req)
	require.NoError(t, err, "request between shrunken TPM and max TPM must be admitted")
	assert.Equal(t, 1, client.completeCalls)

	// Even with the bucket fully drained, the reservation must be satisfiable
	// by waiting for refill rather than rejected outright.
	res := limiter.limiter.ReserveN(time.Now(), count.InputTokens)
	assert.True(t, res.OK(), "request within max TPM must remain reservable after backoffs")
	res.CancelAt(time.Now())
}

func TestAdaptiveRateLimiter_RequestExceedingMaxTPMFailsFast(t *testing.T) {
	limiter := newAdaptiveRateLimiter(600, 600)

	client := &fakeClient{}
	wrapped := limiter.Middleware()(client)

	// ~700 estimated tokens: 600 chars / 3 + 500 overhead, above maxTPM of 600.
	req := requestWithChars(600)

	_, err := wrapped.Complete(context.Background(), &req)
	require.ErrorIs(t, err, ErrRequestTooLarge)
	assert.NotContains(t, err.Error(), "rate: Wait", "must not surface the raw x/time/rate error")
	assert.Contains(t, err.Error(), "max TPM")
	assert.Equal(t, 0, client.completeCalls, "underlying client must not be called")
}

func TestAdaptiveRateLimiter_RefillRateAdjustsBurstStaysPinned(t *testing.T) {
	const (
		initialTPM = 6000.0
		maxTPM     = 12000.0
	)

	tests := []struct {
		name    string
		adjust  func(l *AdaptiveRateLimiter)
		wantTPM float64
	}{
		{
			name: "backoff halves refill rate",
			adjust: func(l *AdaptiveRateLimiter) {
				l.backoff()
			},
			wantTPM: 3000,
		},
		{
			name: "backoff clamps at min TPM",
			adjust: func(l *AdaptiveRateLimiter) {
				for i := 0; i < 10; i++ {
					l.backoff()
				}
			},
			wantTPM: 600,
		},
		{
			name: "probe recovers refill rate",
			adjust: func(l *AdaptiveRateLimiter) {
				l.backoff()
				l.probe()
			},
			wantTPM: 3000 + initialTPM*0.05,
		},
		{
			name: "replaceTPM clamps to range",
			adjust: func(l *AdaptiveRateLimiter) {
				l.replaceTPM(maxTPM * 2)
			},
			wantTPM: maxTPM,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := newAdaptiveRateLimiter(initialTPM, maxTPM)

			tt.adjust(limiter)

			limiter.mu.Lock()
			currentTPM := limiter.currentTPM
			limit := limiter.limiter.Limit()
			burst := limiter.limiter.Burst()
			limiter.mu.Unlock()

			assert.InEpsilon(t, tt.wantTPM, currentTPM, 1e-9)
			assert.InDelta(t, tt.wantTPM/60.0, float64(limit), 1e-9,
				"refill rate must track the effective TPM")
			assert.Equal(t, int(maxTPM), burst,
				"burst must stay pinned at max TPM across adjustments")
		})
	}
}

// requestWithChars builds a single-message request whose text payload has
// exactly n characters, for deterministic token estimation in tests.
func requestWithChars(n int) model.Request {
	return model.Request{
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: strings.Repeat("a", n)},
				},
			},
		},
		MaxTokens: 10,
	}
}

func TestEstimateTokensMonotonic(t *testing.T) {
	t.Helper()

	smallReq := &model.Request{
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: "short"},
				},
			},
		},
	}
	bigReq := &model.Request{
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: "this is a much longer message"},
				},
			},
		},
	}

	estimator := model.TokenEstimator{}
	small, err := estimator.CountTokens(context.Background(), smallReq)
	if err != nil {
		t.Fatalf("expected small token estimate, got error %v", err)
	}
	big, err := estimator.CountTokens(context.Background(), bigReq)
	if err != nil {
		t.Fatalf("expected big token estimate, got error %v", err)
	}

	if small.InputTokens <= 0 {
		t.Fatalf("expected positive token estimate for small request, got %d",
			small.InputTokens)
	}
	if big.InputTokens <= small.InputTokens {
		t.Fatalf("expected larger estimate for larger request, small=%d big=%d",
			small.InputTokens, big.InputTokens)
	}
}
