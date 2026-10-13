package model

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type configurationInfo struct {
	seen    context.Context
	request *Request
}

func (c *configurationInfo) ProviderConfiguration(ctx context.Context, req *Request) string {
	c.seen = ctx
	c.request = req
	return "configured"
}
func TestProviderConfigurationCapabilityCombinations(t *testing.T) {
	for bits := range 16 {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			cfg := &configurationInfo{}
			caps := Capabilities{}
			if bits&1 != 0 {
				caps.Counter = TokenEstimator{}
			}
			if bits&2 != 0 {
				caps.Info = supportContextInfo{}
			}
			if bits&4 != 0 {
				caps.ToolResultParts = &partsInfo{}
			}
			if bits&8 != 0 {
				caps.ProviderConfiguration = cfg
			}
			wrapped := WithCapabilitySet(nil, caps)
			_, counter := wrapped.(TokenCounter)
			_, info := wrapped.(ContextInfo)
			_, parts := wrapped.(ToolResultPartsInfo)
			configured, configuration := wrapped.(ProviderConfigurationInfo)
			assert.Equal(t, bits&1 != 0, counter)
			assert.Equal(t, bits&2 != 0, info)
			assert.Equal(t, bits&4 != 0, parts)
			require.Equal(t, bits&8 != 0, configuration)
			if configuration {
				req := &Request{Model: "exact"}
				ctx := t.Context()
				assert.Equal(t, "configured", configured.ProviderConfiguration(ctx, req))
				assert.Same(t, req, cfg.request)
				assert.Equal(t, ctx, cfg.seen)
			}
		})
	}
	var absent *configurationInfo
	for _, cfg := range []ProviderConfigurationInfo{nil, absent} {
		_, ok := WithCapabilitySet(nil, Capabilities{ProviderConfiguration: cfg}).(ProviderConfigurationInfo)
		assert.False(t, ok)
	}
}

func TestProviderConfigurationValidatedClient(t *testing.T) {
	cfg := &configurationInfo{}
	provider := struct {
		Provider
		ProviderConfigurationInfo
	}{&contractProvider{}, cfg}
	wrapped, err := NewClient(provider)
	require.NoError(t, err)
	info, ok := wrapped.(ProviderConfigurationInfo)
	require.True(t, ok)
	req := &Request{Model: "exact"}
	assert.Equal(t, "configured", info.ProviderConfiguration(t.Context(), req))
	assert.Same(t, req, cfg.request)
}

// providerMethodSets enumerates real optional method sets at the raw boundary.
var providerMethodSets = []Provider{
	struct {
		Provider
	}{&contractProvider{}},
	struct {
		Provider
		TokenCounter
	}{&contractProvider{}, TokenEstimator{}},
	struct {
		Provider
		ContextInfo
	}{&contractProvider{}, supportContextInfo{}},
	struct {
		Provider
		TokenCounter
		ContextInfo
	}{&contractProvider{}, TokenEstimator{}, supportContextInfo{}},
	struct {
		Provider
		ToolResultPartsInfo
	}{&contractProvider{}, &partsInfo{}},
	struct {
		Provider
		TokenCounter
		ToolResultPartsInfo
	}{&contractProvider{}, TokenEstimator{}, &partsInfo{}},
	struct {
		Provider
		ContextInfo
		ToolResultPartsInfo
	}{&contractProvider{}, supportContextInfo{}, &partsInfo{}},
	struct {
		Provider
		TokenCounter
		ContextInfo
		ToolResultPartsInfo
	}{&contractProvider{}, TokenEstimator{}, supportContextInfo{}, &partsInfo{}},
	struct {
		Provider
		ProviderConfigurationInfo
	}{&contractProvider{}, &configurationInfo{}},
	struct {
		Provider
		TokenCounter
		ProviderConfigurationInfo
	}{&contractProvider{}, TokenEstimator{}, &configurationInfo{}},
	struct {
		Provider
		ContextInfo
		ProviderConfigurationInfo
	}{&contractProvider{}, supportContextInfo{}, &configurationInfo{}},
	struct {
		Provider
		TokenCounter
		ContextInfo
		ProviderConfigurationInfo
	}{&contractProvider{}, TokenEstimator{}, supportContextInfo{}, &configurationInfo{}},
	struct {
		Provider
		ToolResultPartsInfo
		ProviderConfigurationInfo
	}{&contractProvider{}, &partsInfo{}, &configurationInfo{}},
	struct {
		Provider
		TokenCounter
		ToolResultPartsInfo
		ProviderConfigurationInfo
	}{&contractProvider{}, TokenEstimator{}, &partsInfo{}, &configurationInfo{}},
	struct {
		Provider
		ContextInfo
		ToolResultPartsInfo
		ProviderConfigurationInfo
	}{&contractProvider{}, supportContextInfo{}, &partsInfo{}, &configurationInfo{}},
	struct {
		Provider
		TokenCounter
		ContextInfo
		ToolResultPartsInfo
		ProviderConfigurationInfo
	}{&contractProvider{}, TokenEstimator{}, supportContextInfo{}, &partsInfo{}, &configurationInfo{}},
}

func TestProviderConfigurationValidatedMethodSets(t *testing.T) {
	for bits, provider := range providerMethodSets {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			wrapped, err := NewClient(provider)
			require.NoError(t, err)
			_, counter := wrapped.(TokenCounter)
			_, info := wrapped.(ContextInfo)
			_, parts := wrapped.(ToolResultPartsInfo)
			_, config := wrapped.(ProviderConfigurationInfo)
			assert.Equal(t, bits&1 != 0, counter)
			assert.Equal(t, bits&2 != 0, info)
			assert.Equal(t, bits&4 != 0, parts)
			assert.Equal(t, bits&8 != 0, config)
		})
	}
}
