package runtime

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnsupportedToolResultNonRetryable(t *testing.T) {
	err := nonRetryableProviderError(&model.UnsupportedPartError{Provider: "openai", Placement: "tool_result", Kind: "image", Reason: "provider"})
	var app *engine.ApplicationError
	require.ErrorAs(t, err, &app)
	assert.True(t, app.NonRetryable)
	assert.ErrorIs(t, err, model.ErrUnsupportedPart)
}
