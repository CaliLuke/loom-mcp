package middleware

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type partsInfoForWrapper struct{}

func (partsInfoForWrapper) ToolResultParts(context.Context, *model.Request) model.ToolResultPartSupport {
	return model.ToolResultPartSupport{MaxImageBytes: 5}
}
func TestToolResultPartsWrapper(t *testing.T) {
	inner := model.WithCapabilitySet(nil, model.Capabilities{ToolResultParts: partsInfoForWrapper{}})
	wrapped := newAdaptiveRateLimiter(1000, 1000).Middleware()(inner)
	info, ok := wrapped.(model.ToolResultPartsInfo)
	require.True(t, ok)
	assert.Equal(t, 5, info.ToolResultParts(t.Context(), nil).MaxImageBytes)
}
