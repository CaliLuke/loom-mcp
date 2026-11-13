package embeddings_test

import (
	"math"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/embeddings"
	"github.com/stretchr/testify/require"
)

func TestValidate(t *testing.T) {
	for _, vector := range [][]float64{nil, {0, 0}, {math.NaN(), 1}, {math.Inf(1), 1}, {1}} {
		require.Error(t, embeddings.Validate(vector, 2))
	}
	require.NoError(t, embeddings.Validate([]float64{0, 1}, 2))
	require.NoError(t, embeddings.Validate([]float64{1}, 0))
}
