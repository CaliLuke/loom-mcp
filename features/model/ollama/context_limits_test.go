package ollama_test

import (
	"context"
	"testing"

	ollamamodel "github.com/CaliLuke/loom-mcp/v2/features/model/ollama"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextLimitsUnknown(t *testing.T) {
	c, err := ollamamodel.New(ollamamodel.Options{ServerURL: "http://localhost:11434", DefaultModel: "local", MaxTokens: 128})
	require.NoError(t, err)
	info, ok := any(c).(model.ContextInfo)
	require.True(t, ok)
	limits, resolved, known := info.ContextLimits(context.Background(), &model.Request{})
	assert.False(t, known)
	assert.Equal(t, "local", resolved)
	assert.Equal(t, model.ContextLimits{DefaultMaxTokens: 128}, limits)
}
