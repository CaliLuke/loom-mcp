package anthropic

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderConfigurationIdentity(t *testing.T) {
	cfg, ok := any(&Client{}).(model.ProviderConfigurationInfo)
	require.True(t, ok, "adapter must identify its provider configuration")
	for _, tc := range []struct{ model, config string }{{"exact", "anthropic"}} {
		assert.Equal(t, tc.config, cfg.ProviderConfiguration(t.Context(), &model.Request{Model: tc.model}))
	}
}
