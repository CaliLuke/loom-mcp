package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/features/model/gateway"
	"github.com/CaliLuke/loom-mcp/v2/features/model/ollama"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contextLimitsClient struct {
	contractModelClient
	countErr error
}

func (contextLimitsClient) ContextLimits(_ context.Context, req *model.Request) (model.ContextLimits, string, bool) {
	return model.ContextLimits{ContextWindow: 1000, MaxOutputTokens: 100, DefaultMaxTokens: 50}, req.Model, true
}

func (c contextLimitsClient) CountTokens(context.Context, *model.Request) (model.TokenCount, error) {
	return model.TokenCount{InputTokens: 12, Exact: true}, c.countErr
}

func TestContextLimitsOverrides(t *testing.T) {
	overrides := map[string]model.ContextLimits{
		"resolved":   {ContextWindow: 2000, ImageTokens: 30},
		"registered": {ContextWindow: 3000, MaxOutputTokens: 200, DefaultMaxTokens: 75},
	}
	rt := New(WithContextLimits(overrides))
	require.NoError(t, rt.RegisterModel("registered", contextLimitsClient{}))
	overrides["resolved"] = model.ContextLimits{ContextWindow: 99}
	client, found := rt.ModelClient("registered")
	require.True(t, found)
	info, ok := client.(model.ContextInfo)
	require.True(t, ok)
	for _, tc := range []struct {
		id   string
		want model.ContextLimits
	}{
		{"resolved", model.ContextLimits{ContextWindow: 2000, MaxOutputTokens: 100, DefaultMaxTokens: 50, ImageTokens: 30}},
		{"other", model.ContextLimits{ContextWindow: 3000, MaxOutputTokens: 200, DefaultMaxTokens: 75}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			limits, resolved, known := info.ContextLimits(context.Background(), &model.Request{Model: tc.id})
			assert.True(t, known)
			assert.Equal(t, tc.id, resolved)
			assert.Equal(t, tc.want, limits)
		})
	}
	counter, ok := client.(model.TokenCounter)
	require.True(t, ok)
	count, err := counter.CountTokens(context.Background(), &model.Request{})
	require.NoError(t, err)
	assert.Equal(t, 12, count.InputTokens)
}

func TestContextLimitsOverridesUnknownClient(t *testing.T) {
	for _, tc := range []struct {
		name      string
		overrides map[string]model.ContextLimits
		known     bool
	}{
		{"no override", nil, false},
		{"resolved", map[string]model.ContextLimits{"request-model": {ContextWindow: 1000}}, true},
		{"registered fallback", map[string]model.ContextLimits{"registered": {ContextWindow: 1000}}, true},
		{"no window", map[string]model.ContextLimits{"registered": {ImageTokens: 30}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New(WithContextLimits(tc.overrides))
			require.NoError(t, rt.RegisterModel("registered", contractModelClient{}))
			client, found := rt.ModelClient("registered")
			require.True(t, found)
			info, ok := client.(model.ContextInfo)
			if !ok {
				assert.False(t, tc.known)
				return
			}
			limits, resolved, known := info.ContextLimits(context.Background(), &model.Request{Model: "request-model"})
			assert.Equal(t, tc.known, known)
			assert.Equal(t, "request-model", resolved)
			if tc.known {
				assert.Equal(t, 1000, limits.ContextWindow)
			}
			_, hasCounter := client.(model.TokenCounter)
			assert.False(t, hasCounter)
		})
	}
}

func TestContextLimitsOverrideValidation(t *testing.T) {
	for _, limits := range []model.ContextLimits{
		{ContextWindow: -1}, {MaxOutputTokens: -1}, {DefaultMaxTokens: -1}, {ImageTokens: -1},
		{ContextWindow: 100, MaxOutputTokens: 101}, {MaxOutputTokens: 1},
	} {
		assert.Panics(t, func() {
			newFromOptions(Options{ContextLimits: map[string]model.ContextLimits{"model": limits}})
		})
	}
	assert.NotPanics(t, func() {
		New(WithContextLimits(map[string]model.ContextLimits{"model": {ContextWindow: 100, MaxOutputTokens: 100}}))
	})
}

func TestContextLimitsOverridesOllamaAndGateway(t *testing.T) {
	provider, err := ollama.New(ollama.Options{ServerURL: "http://localhost:11434", DefaultModel: "local", MaxTokens: 128})
	require.NoError(t, err)
	local, err := model.NewClient(provider)
	require.NoError(t, err)
	remote := gateway.NewRemoteClient(
		func(context.Context, *model.Request) (*model.Response, error) {
			return nil, nil
		},
		func(context.Context, *model.Request) (model.Streamer, error) {
			return nil, model.ErrStreamingUnsupported
		},
	)
	rt := New(WithContextLimits(map[string]model.ContextLimits{
		"local": {ContextWindow: 1000}, "gateway": {ContextWindow: 2000},
	}))
	for _, tc := range []struct {
		id                 string
		client             model.Client
		window, defaultMax int
	}{
		{"ollama", local, 1000, 128}, {"gateway", remote, 2000, 0},
	} {
		t.Run(tc.id, func(t *testing.T) {
			require.NoError(t, rt.RegisterModel(tc.id, tc.client))
			client, found := rt.ModelClient(tc.id)
			require.True(t, found)
			info, ok := client.(model.ContextInfo)
			require.True(t, ok)
			limits, _, known := info.ContextLimits(context.Background(), &model.Request{})
			assert.True(t, known)
			assert.Equal(t, tc.window, limits.ContextWindow)
			assert.Equal(t, tc.defaultMax, limits.DefaultMaxTokens)
		})
	}
}
