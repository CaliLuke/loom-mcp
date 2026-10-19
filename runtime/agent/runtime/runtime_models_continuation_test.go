package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeGeminiContinuationLimits(t *testing.T) {
	rt := New()
	client, err := rt.NewGeminiModelClient(t.Context(), GeminiConfig{APIKey: "test", DefaultModel: "test", MaxGenerationRequests: -1})
	require.ErrorContains(t, err, "generation requests")
	assert.Nil(t, client)
	client, err = rt.NewVertexGeminiModelClient(t.Context(), VertexConfig{APIKey: "test", DefaultModel: "test", MaxGenerationRequests: -1})
	require.ErrorContains(t, err, "generation requests")
	assert.Nil(t, client)
}
