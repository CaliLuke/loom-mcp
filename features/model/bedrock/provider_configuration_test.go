package bedrock

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderConfigurationIdentity(t *testing.T) {
	cfg, ok := any(&Client{}).(model.ProviderConfigurationInfo)
	require.True(t, ok, "adapter must identify its provider configuration")
	for _, tc := range []struct{ model, config string }{{"anthropic.claude-sonnet-4-20250514-v1:0", "bedrock-anthropic"}, {"us.anthropic.claude-sonnet-4-20250514-v1:0", "bedrock-anthropic"}, {"amazon.nova-pro-v1:0", "bedrock"}} {
		assert.Equal(t, tc.config, cfg.ProviderConfiguration(t.Context(), &model.Request{Model: tc.model}))
	}
}

func TestProviderConfigurationResolvedIdentity(t *testing.T) {
	client := &Client{defaultModel: "us.anthropic.claude-sonnet-4-20250514-v1:0"}
	_, resolved, _ := client.ContextLimits(t.Context(), &model.Request{})
	assert.Equal(t, "anthropic.claude-sonnet-4-20250514-v1:0", resolved)
}
