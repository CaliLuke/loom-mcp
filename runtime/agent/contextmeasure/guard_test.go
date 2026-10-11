package contextmeasure

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
)

type fakeCounter struct {
	count model.TokenCount
	err   error
	calls int
	req   *model.Request
}

func TestGuardTokens(t *testing.T) {
	req := &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "abcdef"}}}}}
	for _, tc := range []struct {
		name                              string
		budget                            int
		calibrated, exact                 bool
		count                             int
		err                               error
		wantTokens                        int
		wantCounted, wantExact, wantGuard bool
	}{
		{"below", 10, true, true, 99, nil, 2, false, false, true},
		{"equal", 4, true, true, 99, nil, 2, false, false, true},
		{"above_exact_before_calibration", 3, false, true, 11, nil, 11, true, true, true},
		{"above_inexact", 3, true, false, 99, nil, 2, true, false, true},
		{"missing_usage_inexact", 3, false, false, 99, nil, 2, true, false, false},
		{"counter_error", 3, true, true, 99, errors.New("counter failed"), 2, true, false, true},
		{"unknown_limits", 0, true, true, 11, nil, 2, false, false, false},
		{"negative_budget", -1, true, true, 11, nil, 2, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			counter := &fakeCounter{count: model.TokenCount{InputTokens: tc.count, Exact: tc.exact}, err: tc.err}
			got := GuardTokens(context.Background(), counter, req, Rate{1, 3}, tc.calibrated, GuardBudget{Tokens: tc.budget}, Rate{1, 2})
			assert.Equal(t, tc.wantTokens, got.Tokens)
			assert.Equal(t, tc.wantCounted, got.Counted)
			assert.Equal(t, tc.wantExact, got.Exact)
			assert.Equal(t, tc.wantGuard, got.GuardOn)
			wantCalls := 0
			if tc.wantCounted {
				wantCalls = 1
				assert.Same(t, req, counter.req)
				assert.GreaterOrEqual(t, got.Latency, time.Duration(0))
			} else {
				assert.Zero(t, got.Latency, "latency recorded without a count")
			}
			assert.Equal(t, wantCalls, counter.calls)
		})
	}
}

func TestGuardThinkingAndImageCosts(t *testing.T) {
	req := &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{
		model.ThinkingPart{Text: "abcd", Redacted: []byte{1}, Signature: "excluded"},
		&model.ThinkingPart{Text: "e"},
	}}, {Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte{1}}}}}}
	counter := &fakeCounter{count: model.TokenCount{InputTokens: 10, Exact: true}}
	got := GuardTokens(context.Background(), counter, req, Rate{1, 3}, false, GuardBudget{Tokens: 100, ImageTokens: 40}, Rate{1, 2})
	assert.Zero(t, counter.calls)
	assert.Equal(t, 42, got.Tokens)
	assert.False(t, got.GuardOn)
	got = GuardTokens(context.Background(), counter, req, Rate{1, 3}, false, GuardBudget{Tokens: 70, ImageTokens: 40}, Rate{1, 2})
	assert.Equal(t, 1, counter.calls)
	assert.Equal(t, 12, got.Tokens)
	assert.True(t, got.Exact)
	assert.True(t, got.GuardOn)
}

func TestGuardNoCounterAndBounds(t *testing.T) {
	req := &model.Request{Messages: []*model.Message{{Parts: []model.Part{model.TextPart{Text: "abc"}}}}}
	for _, calibrated := range []bool{false, true} {
		got := GuardTokens(context.Background(), nil, req, Rate{1, 3}, calibrated, GuardBudget{Tokens: 1}, Rate{1, 2})
		assert.Equal(t, GuardCount{Tokens: 1, GuardOn: calibrated}, got)
	}
	counter := &fakeCounter{count: model.TokenCount{InputTokens: math.MaxInt, Exact: true}}
	req.Messages[0].Parts = []model.Part{model.ThinkingPart{Text: "abc"}}
	got := GuardTokens(context.Background(), counter, req, Rate{1, 3}, false, GuardBudget{Tokens: 1}, Rate{1, 2})
	assert.Equal(t, math.MaxInt, got.Tokens)
	assert.True(t, got.Exact)
	counter.count.InputTokens = -1
	got = GuardTokens(context.Background(), counter, req, Rate{1, 3}, false, GuardBudget{Tokens: 1}, Rate{1, 2})
	assert.Equal(t, 1, got.Tokens)
	assert.False(t, got.Exact)
	assert.False(t, got.GuardOn)
	// The threshold is fractional: 1 > 1*1/2 even though ceil(1*1/2)=1.
	assert.True(t, got.Counted, "rounded threshold hid a request above the fractional trigger")
}

func TestGuardTokensZeroTriggerNeverCounts(t *testing.T) {
	req := &model.Request{Messages: []*model.Message{{Parts: []model.Part{model.TextPart{Text: "abcdef"}}}}}
	for _, calibrated := range []bool{false, true} {
		counter := &fakeCounter{count: model.TokenCount{InputTokens: 99, Exact: true}}
		got := GuardTokens(t.Context(), counter, req, Rate{}, calibrated, GuardBudget{Tokens: 1}, Rate{})
		assert.Equal(t, GuardCount{Tokens: 2, GuardOn: calibrated}, got)
		assert.Zero(t, counter.calls)
	}
}

func (c *fakeCounter) CountTokens(ctx context.Context, req *model.Request) (model.TokenCount, error) {
	c.calls++
	c.req = req
	return c.count, c.err
}
