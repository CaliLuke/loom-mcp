package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type limitsProvider struct {
	*contractProvider
}

func (*limitsProvider) ContextLimits(_ context.Context, req *Request) (ContextLimits, string, bool) {
	return ContextLimits{ContextWindow: 1000, MaxOutputTokens: 100, DefaultMaxTokens: 50, ImageTokens: 25}, req.Model, true
}

func TestNewClientPreservesContextCapabilities(t *testing.T) {
	plain := &contractProvider{}
	counter := &contractCountingProvider{contractProvider: plain, count: TokenCount{InputTokens: 7, Exact: true}}
	info := &limitsProvider{contractProvider: plain}
	for _, tc := range []struct {
		name             string
		provider         Provider
		counting, limits bool
	}{
		{"neither", plain, false, false},
		{"counter", counter, true, false},
		{"limits", info, false, true},
		{"both", struct {
			Provider
			TokenCounter
			ContextInfo
		}{plain, counter, info}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewClient(tc.provider)
			require.NoError(t, err)
			_, hasCounter := client.(TokenCounter)
			assert.Equal(t, tc.counting, hasCounter)
			capability, hasLimits := client.(ContextInfo)
			assert.Equal(t, tc.limits, hasLimits)
			if hasLimits {
				limits, resolved, known := capability.ContextLimits(context.Background(), &Request{Model: "resolved"})
				assert.True(t, known)
				assert.Equal(t, "resolved", resolved)
				assert.Equal(t, ContextLimits{ContextWindow: 1000, MaxOutputTokens: 100, DefaultMaxTokens: 50, ImageTokens: 25}, limits)
			}
		})
	}
}
