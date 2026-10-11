package middleware

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
)

type limitsCapability struct{}

func (limitsCapability) ContextLimits(_ context.Context, req *model.Request) (model.ContextLimits, string, bool) {
	return model.ContextLimits{ContextWindow: 1000, MaxOutputTokens: 100}, req.Model, true
}

func TestMiddlewarePreservesContextCapabilities(t *testing.T) {
	plain := &fakeClient{}
	for _, tc := range []struct {
		name             string
		client           model.Client
		counting, limits bool
	}{
		{"neither", plain, false, false},
		{"counter", struct {
			model.Client
			model.TokenCounter
		}{plain, model.TokenEstimator{}}, true, false},
		{"limits", struct {
			model.Client
			model.ContextInfo
		}{plain, limitsCapability{}}, false, true},
		{"both", struct {
			model.Client
			model.TokenCounter
			model.ContextInfo
		}{plain, model.TokenEstimator{}, limitsCapability{}}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limiter := newAdaptiveRateLimiter(1000, 1000)
			wrapped := limiter.Middleware()(tc.client)
			_, counter := wrapped.(model.TokenCounter)
			assert.Equal(t, tc.counting, counter)
			info, hasLimits := wrapped.(model.ContextInfo)
			assert.Equal(t, tc.limits, hasLimits)
			if hasLimits {
				limits, resolved, known := info.ContextLimits(context.Background(), &model.Request{Model: "resolved"})
				assert.True(t, known)
				assert.Equal(t, "resolved", resolved)
				assert.Equal(t, 1000, limits.ContextWindow)
				assert.InDelta(t, 1000, limiter.limiter.Tokens(), 0.001)
			}
		})
	}
}
