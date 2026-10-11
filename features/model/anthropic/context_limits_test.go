package anthropic

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type limitsModelsFunc func(context.Context, string) (*sdk.ModelInfo, error)

func (f limitsModelsFunc) Get(ctx context.Context, id string, _ sdk.ModelGetParams, _ ...option.RequestOption) (*sdk.ModelInfo, error) {
	return f(ctx, id)
}

func TestContextLimitsTable(t *testing.T) {
	c, err := New(&stubMessagesClient{}, Options{DefaultModel: sdk.ModelClaudeOpus5_5, MaxTokens: 128})
	require.NoError(t, err)
	for _, id := range []string{sdk.ModelClaudeOpus5_5, sdk.ModelClaudeSonnet5_5, sdk.ModelClaudeHaiku5_5, sdk.ModelClaudeFable5_1} {
		t.Run(id, func(t *testing.T) {
			limits, resolved, known := c.ContextLimits(context.Background(), &model.Request{Model: id})
			assert.True(t, known)
			assert.Equal(t, id, resolved)
			assert.Equal(t, model.ContextLimits{ContextWindow: 1000000, MaxOutputTokens: 128000, DefaultMaxTokens: 128}, limits)
		})
	}
	limits, resolved, known := c.ContextLimits(context.Background(), &model.Request{Model: "unknown"})
	assert.False(t, known)
	assert.Equal(t, "unknown", resolved)
	assert.Equal(t, model.ContextLimits{DefaultMaxTokens: 128}, limits)
}

func TestContextLimitsRecordedAPIAndCache(t *testing.T) {
	body, err := os.ReadFile("testdata/context_limits.json")
	require.NoError(t, err)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/v1/models/claude-opus-5-5", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, writeErr := w.Write(body)
		assert.NoError(t, writeErr)
	}))
	t.Cleanup(server.Close)
	api := sdk.NewClient(option.WithAPIKey("test"), option.WithBaseURL(server.URL), option.WithMaxRetries(0))
	c, err := New(&stubMessagesClient{}, Options{DefaultModel: sdk.ModelClaudeOpus5_5, Models: &api.Models, MaxTokens: 128})
	require.NoError(t, err)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			limits, resolved, known := c.ContextLimits(context.Background(), &model.Request{})
			assert.True(t, known)
			assert.Equal(t, "claude-opus-5-5", resolved)
			assert.Equal(t, model.ContextLimits{ContextWindow: 1000128, MaxOutputTokens: 128000, DefaultMaxTokens: 128}, limits)
		})
	}
	group.Wait()
	assert.Equal(t, int32(1), calls.Load())
}

func TestContextLimitsAPIFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result *sdk.ModelInfo
		err    error
	}{
		{"error", nil, errors.New("unavailable")},
		{"nil", nil, nil},
		{"missing limits", &sdk.ModelInfo{ID: "resolved"}, nil},
		{"negative limits", &sdk.ModelInfo{MaxInputTokens: -1, MaxTokens: 128}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			models := limitsModelsFunc(func(ctx context.Context, _ string) (*sdk.ModelInfo, error) {
				calls++
				deadline, ok := ctx.Deadline()
				assert.True(t, ok)
				assert.LessOrEqual(t, time.Until(deadline), 2*time.Second)
				return tc.result, tc.err
			})
			c, err := New(&stubMessagesClient{}, Options{DefaultModel: sdk.ModelClaudeOpus5_5, Models: models})
			require.NoError(t, err)
			for range 2 {
				limits, _, known := c.ContextLimits(context.Background(), &model.Request{})
				assert.True(t, known)
				assert.Equal(t, 1000000, limits.ContextWindow)
			}
			assert.Equal(t, 1, calls)
			_, resolved, known := c.ContextLimits(context.Background(), &model.Request{Model: "unknown"})
			assert.False(t, known)
			assert.Equal(t, "unknown", resolved)
			assert.Equal(t, 2, calls)
		})
	}
}

func TestContextLimitsDeadlineAndResolvedModel(t *testing.T) {
	c, err := New(&stubMessagesClient{}, Options{DefaultModel: "alias", Models: limitsModelsFunc(func(ctx context.Context, _ string) (*sdk.ModelInfo, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, resolved, known := c.ContextLimits(ctx, &model.Request{})
	assert.False(t, known)
	assert.Equal(t, "alias", resolved)
	c, err = New(&stubMessagesClient{}, Options{DefaultModel: "alias", Models: limitsModelsFunc(func(context.Context, string) (*sdk.ModelInfo, error) {
		return &sdk.ModelInfo{ID: "canonical", MaxInputTokens: 1000, MaxTokens: 100}, nil
	})})
	require.NoError(t, err)
	_, resolved, known = c.ContextLimits(context.Background(), &model.Request{})
	assert.True(t, known)
	assert.Equal(t, "canonical", resolved)
}

func TestContextLimitsRoutingAndClientIsolation(t *testing.T) {
	calls := make(map[string]int)
	api := limitsModelsFunc(func(_ context.Context, id string) (*sdk.ModelInfo, error) {
		calls[id]++
		return &sdk.ModelInfo{ID: id, MaxInputTokens: 1000, MaxTokens: 100}, nil
	})
	opts := Options{DefaultModel: "default", HighModel: "high", SmallModel: "small", Models: api}
	c, err := New(&stubMessagesClient{}, opts)
	require.NoError(t, err)
	for _, tc := range []struct {
		req model.Request
		id  string
	}{
		{model.Request{}, "default"},
		{model.Request{ModelClass: model.ModelClassHighReasoning}, "high"},
		{model.Request{ModelClass: model.ModelClassSmall}, "small"},
		{model.Request{Model: "explicit", ModelClass: model.ModelClassSmall}, "explicit"},
	} {
		for range 2 {
			_, resolved, known := c.ContextLimits(context.Background(), &tc.req)
			assert.True(t, known)
			assert.Equal(t, tc.id, resolved)
		}
	}
	another, err := New(&stubMessagesClient{}, opts)
	require.NoError(t, err)
	_, _, known := another.ContextLimits(context.Background(), &model.Request{})
	assert.True(t, known)
	assert.Equal(t, map[string]int{"default": 2, "high": 1, "small": 1, "explicit": 1}, calls)
}

func TestContextLimitsInputAllowancePerRequest(t *testing.T) {
	for _, tc := range []struct {
		name          string
		configured    int
		input, output int64
		requests      []int
	}{
		{"no_default", 0, 1000000, 128000, []int{32000, 0}},
		{"configured", 128, 1000000, 128000, []int{0, 32000, 512}},
		{"quarter_fallback", 0, 1000, 400, []int{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c, err := New(&stubMessagesClient{}, Options{DefaultModel: "alias", MaxTokens: tc.configured, Models: limitsModelsFunc(func(context.Context, string) (*sdk.ModelInfo, error) {
				calls++
				return &sdk.ModelInfo{ID: "resolved", MaxInputTokens: tc.input, MaxTokens: tc.output}, nil
			})})
			require.NoError(t, err)
			for _, requested := range tc.requests {
				req := &model.Request{MaxTokens: requested}
				limits, resolved, known := c.ContextLimits(t.Context(), req)
				assert.True(t, known)
				assert.Equal(t, "resolved", resolved)
				assert.Equal(t, tc.configured, limits.DefaultMaxTokens)
				reserve := contextmeasure.OutputReserve(req, limits)
				budget, usable := contextmeasure.Budget(limits, reserve)
				assert.True(t, usable)
				assert.Equal(t, int(tc.input), budget)
				assert.Equal(t, int(tc.input)+reserve, limits.ContextWindow)
			}
			assert.Equal(t, 1, calls)
		})
	}
}
