package gateway

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
)

func TestProviderConfigurationUnknown(t *testing.T) {
	if cfg, ok := any(&RemoteClient{}).(model.ProviderConfigurationInfo); ok {
		assert.Empty(t, cfg.ProviderConfiguration(t.Context(), &model.Request{Model: "unknown"}))
	}
}
