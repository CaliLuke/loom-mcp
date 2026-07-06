package runtime

import (
	"context"
	"reflect"
	"testing"

	geminifeature "github.com/CaliLuke/loom-mcp/features/model/gemini"
	"github.com/stretchr/testify/require"
)

func TestNewGeminiModelClientRequiresAPIKey(t *testing.T) {
	rt := &Runtime{}

	client, err := rt.NewGeminiModelClient(context.Background(), GeminiConfig{DefaultModel: "gemini-2.5-flash"})
	require.Error(t, err)
	require.Nil(t, client)
	require.Contains(t, err.Error(), "api key is required")
}

func TestNewGeminiModelClientBuildsSDKClient(t *testing.T) {
	rt := &Runtime{}

	client, err := rt.NewGeminiModelClient(context.Background(), GeminiConfig{
		APIKey:       "test-key",
		DefaultModel: "gemini-2.5-flash",
		HighModel:    "gemini-2.5-pro",
		SmallModel:   "gemini-2.5-flash-lite",
		MaxTokens:    1024,
		Temperature:  0.2,
	})
	require.NoError(t, err)

	geminiClient, ok := client.(*geminifeature.Client)
	require.True(t, ok)
	value := reflect.ValueOf(geminiClient).Elem()
	require.Equal(t, "gemini-2.5-flash", value.FieldByName("defaultModel").String())
	require.Equal(t, "gemini-2.5-pro", value.FieldByName("highModel").String())
	require.Equal(t, "gemini-2.5-flash-lite", value.FieldByName("smallModel").String())
}

func TestNewVertexGeminiModelClientValidates(t *testing.T) {
	rt := &Runtime{}

	client, err := rt.NewVertexGeminiModelClient(context.Background(), VertexConfig{
		Location:     "global",
		DefaultModel: "gemini-2.5-pro",
	})
	require.Error(t, err)
	require.Nil(t, client)
	require.Contains(t, err.Error(), "project id is required")

	client, err = rt.NewVertexGeminiModelClient(context.Background(), VertexConfig{
		ProjectID:    "project",
		DefaultModel: "gemini-2.5-pro",
	})
	require.Error(t, err)
	require.Nil(t, client)
	require.Contains(t, err.Error(), "location is required")

	client, err = rt.NewVertexGeminiModelClient(context.Background(), VertexConfig{
		ProjectID: "project",
		Location:  "global",
	})
	require.Error(t, err)
	require.Nil(t, client)
	require.Contains(t, err.Error(), "default model is required")
}
