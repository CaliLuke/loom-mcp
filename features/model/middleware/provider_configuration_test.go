package middleware

import (
	"context"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type configurationWrapperInfo struct {
	request *model.Request
	ctx     context.Context
}

func (c *configurationWrapperInfo) ProviderConfiguration(ctx context.Context, req *model.Request) string {
	c.request = req
	c.ctx = ctx
	return "exact"
}
func TestProviderConfigurationWrapper(t *testing.T) {
	cfg := &configurationWrapperInfo{}
	inner := struct {
		model.Client
		model.ProviderConfigurationInfo
	}{nil, cfg}
	wrapped := newAdaptiveRateLimiter(1000, 1000).Middleware()(inner)
	info, ok := wrapped.(model.ProviderConfigurationInfo)
	require.True(t, ok)
	req := &model.Request{Model: "resolved"}
	ctx := t.Context()
	assert.Equal(t, "exact", info.ProviderConfiguration(ctx, req))
	assert.Same(t, req, cfg.request)
	assert.Equal(t, ctx, cfg.ctx)
}

type allWrapperCapabilities struct {
	model.Client
	*configurationWrapperInfo
}

func (allWrapperCapabilities) ContextLimits(context.Context, *model.Request) (model.ContextLimits, string, bool) {
	return model.ContextLimits{}, "", false
}
func (allWrapperCapabilities) ToolResultParts(context.Context, *model.Request) model.ToolResultPartSupport {
	return model.ToolResultPartSupport{}
}
func TestProviderConfigurationWrapperMethodSets(t *testing.T) {
	for bits := range 16 {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			probe := &allWrapperCapabilities{configurationWrapperInfo: &configurationWrapperInfo{}}
			caps := model.Capabilities{}
			if bits&1 != 0 {
				caps.Counter = model.TokenEstimator{}
			}
			if bits&2 != 0 {
				caps.Info = probe
			}
			if bits&4 != 0 {
				caps.ToolResultParts = probe
			}
			if bits&8 != 0 {
				caps.ProviderConfiguration = probe
			}
			inner := model.WithCapabilitySet(&struct{ model.Client }{}, caps)
			wrapped := newAdaptiveRateLimiter(1000, 1000).Middleware()(inner)
			_, counter := wrapped.(model.TokenCounter)
			_, info := wrapped.(model.ContextInfo)
			_, parts := wrapped.(model.ToolResultPartsInfo)
			_, config := wrapped.(model.ProviderConfigurationInfo)
			assert.Equal(t, bits&1 != 0, counter)
			assert.Equal(t, bits&2 != 0, info)
			assert.Equal(t, bits&4 != 0, parts)
			assert.Equal(t, bits&8 != 0, config)
		})
	}
}
