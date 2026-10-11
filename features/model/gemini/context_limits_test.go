package gemini_test

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

	geminimodel "github.com/CaliLuke/loom-mcp/v2/features/model/gemini"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

type limitsModelsClient struct {
	mockModelsClient
	get func(context.Context, string) (*genai.Model, error)
}

func (c *limitsModelsClient) Get(ctx context.Context, id string, _ *genai.GetModelConfig) (*genai.Model, error) {
	return c.get(ctx, id)
}

func TestContextLimitsRecordedAPIAndCache(t *testing.T) {
	body, err := os.ReadFile("testdata/context_limits.json")
	require.NoError(t, err)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/v1beta/models/gemini-2.5-flash", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, writeErr := w.Write(body)
		assert.NoError(t, writeErr)
	}))
	t.Cleanup(server.Close)
	api, err := genai.NewClient(context.Background(), &genai.ClientConfig{APIKey: "test", Backend: genai.BackendGeminiAPI, HTTPOptions: genai.HTTPOptions{BaseURL: server.URL}})
	require.NoError(t, err)
	c, err := geminimodel.New(geminimodel.Options{Client: api.Models, DefaultModel: "gemini-2.5-flash", MaxTokens: 128})
	require.NoError(t, err)
	info, ok := any(c).(model.ContextInfo)
	require.True(t, ok)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			limits, resolved, known := info.ContextLimits(context.Background(), &model.Request{})
			assert.True(t, known)
			assert.Equal(t, "gemini-2.5-flash", resolved)
			assert.Equal(t, model.ContextLimits{ContextWindow: 1048704, MaxOutputTokens: 65536, DefaultMaxTokens: 128}, limits)
		})
	}
	group.Wait()
	assert.Equal(t, int32(1), calls.Load())
}

func TestContextLimitsUnknownFallback(t *testing.T) {
	c, err := geminimodel.New(geminimodel.Options{Client: &mockModelsClient{}, DefaultModel: "configured", MaxTokens: 128})
	require.NoError(t, err)
	info, ok := any(c).(model.ContextInfo)
	require.True(t, ok)
	limits, resolved, known := info.ContextLimits(context.Background(), &model.Request{})
	assert.False(t, known)
	assert.Equal(t, "configured", resolved)
	assert.Equal(t, model.ContextLimits{DefaultMaxTokens: 128}, limits)
}

func TestContextLimitsAPIFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result *genai.Model
		err    error
	}{
		{"error", nil, errors.New("unavailable")}, {"nil", nil, nil},
		{"missing limits", &genai.Model{}, nil}, {"negative limits", &genai.Model{InputTokenLimit: -1, OutputTokenLimit: 100}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			api := &limitsModelsClient{get: func(ctx context.Context, _ string) (*genai.Model, error) {
				calls++
				deadline, ok := ctx.Deadline()
				assert.True(t, ok)
				assert.LessOrEqual(t, time.Until(deadline), 2*time.Second)
				return tc.result, tc.err
			}}
			c, err := geminimodel.New(geminimodel.Options{Client: api, DefaultModel: "configured"})
			require.NoError(t, err)
			info, ok := any(c).(model.ContextInfo)
			require.True(t, ok)
			for range 2 {
				_, resolved, known := info.ContextLimits(context.Background(), &model.Request{})
				assert.False(t, known)
				assert.Equal(t, "configured", resolved)
			}
			assert.Equal(t, 1, calls)
		})
	}
}

func TestContextLimitsResolvedModelAndDeadline(t *testing.T) {
	var ids []string
	api := &limitsModelsClient{get: func(ctx context.Context, id string) (*genai.Model, error) {
		if id == "blocked" {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		ids = append(ids, id)
		return &genai.Model{Name: "models/" + id, InputTokenLimit: 1000, OutputTokenLimit: 100}, nil
	}}
	c, err := geminimodel.New(geminimodel.Options{Client: api, DefaultModel: "default", HighModel: "high", SmallModel: "small"})
	require.NoError(t, err)
	info, ok := any(c).(model.ContextInfo)
	require.True(t, ok)
	for _, tc := range []struct {
		req model.Request
		id  string
	}{
		{model.Request{}, "default"}, {model.Request{ModelClass: model.ModelClassHighReasoning}, "high"},
		{model.Request{ModelClass: model.ModelClassSmall}, "small"}, {model.Request{Model: "explicit", ModelClass: model.ModelClassSmall}, "explicit"},
	} {
		_, resolved, known := info.ContextLimits(context.Background(), &tc.req)
		assert.True(t, known)
		assert.Equal(t, tc.id, resolved)
	}
	another, err := geminimodel.New(geminimodel.Options{Client: api, DefaultModel: "default"})
	require.NoError(t, err)
	anotherInfo, ok := any(another).(model.ContextInfo)
	require.True(t, ok)
	_, _, known := anotherInfo.ContextLimits(context.Background(), &model.Request{})
	assert.True(t, known)
	assert.Equal(t, []string{"default", "high", "small", "explicit", "default"}, ids)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, resolved, known := info.ContextLimits(ctx, &model.Request{Model: "blocked"})
	assert.False(t, known)
	assert.Equal(t, "blocked", resolved)
}

func TestContextLimitsInputAllowancePerRequest(t *testing.T) {
	for _, tc := range []struct {
		name          string
		configured    int
		input, output int32
		requests      []int
	}{
		{"no_default", 0, 1048576, 65536, []int{0, 32000}},
		{"configured", 128, 1048576, 65536, []int{0, 32000, 512}},
		{"quarter_fallback", 0, 1000, 400, []int{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			api := &limitsModelsClient{get: func(context.Context, string) (*genai.Model, error) {
				calls++
				return &genai.Model{Name: "models/resolved", InputTokenLimit: tc.input, OutputTokenLimit: tc.output}, nil
			}}
			c, err := geminimodel.New(geminimodel.Options{Client: api, DefaultModel: "alias", MaxTokens: tc.configured})
			require.NoError(t, err)
			info, ok := any(c).(model.ContextInfo)
			require.True(t, ok)
			for _, requested := range tc.requests {
				req := &model.Request{MaxTokens: requested}
				limits, resolved, known := info.ContextLimits(t.Context(), req)
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
