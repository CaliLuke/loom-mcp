package env_test

import (
	"example.com/longrun/env"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRenderedAssets(t *testing.T) {
	for _, format := range []string{"png", "pdf"} {
		data, err := env.RenderAsset(format, "IMG-19AF0")
		require.NoError(t, err)
		token, err := env.AssetToken(format, data)
		require.NoError(t, err)
		assert.Equal(t, "IMG-19AF0", token)
	}
}
