package runtime

import (
	"context"
	"reflect"
	"testing"

	"cloud.google.com/go/auth"
	geminifeature "github.com/CaliLuke/loom-mcp/features/model/gemini"
	ollamafeature "github.com/CaliLuke/loom-mcp/features/model/ollama"
	openaifeature "github.com/CaliLuke/loom-mcp/features/model/openai"
	"github.com/stretchr/testify/require"
)

func TestNewOpenAIModelClientRequiresAPIKey(t *testing.T) {
	rt := &Runtime{}

	client, err := rt.NewOpenAIModelClient(OpenAIConfig{DefaultModel: "gpt-4o"})
	require.Error(t, err)
	require.Nil(t, client)
	require.Contains(t, err.Error(), "api key is required")
}

func TestNewOpenAIModelClientBuildsSDKClient(t *testing.T) {
	rt := &Runtime{}

	client, err := rt.NewOpenAIModelClient(OpenAIConfig{
		APIKey:       "sk-test",
		DefaultModel: "gpt-4o",
	})
	require.NoError(t, err)

	openaiClient, ok := client.(*openaifeature.Client)
	require.True(t, ok)
	value := reflect.ValueOf(openaiClient).Elem()
	require.Equal(t, "gpt-4o", value.FieldByName("model").String())
}

func TestNewOllamaModelClientValidates(t *testing.T) {
	rt := &Runtime{}

	client, err := rt.NewOllamaModelClient(OllamaConfig{DefaultModel: "llama3.1"})
	require.Error(t, err)
	require.Nil(t, client)
	require.Contains(t, err.Error(), "server URL is required")

	client, err = rt.NewOllamaModelClient(OllamaConfig{ServerURL: "http://localhost:11434"})
	require.Error(t, err)
	require.Nil(t, client)
	require.Contains(t, err.Error(), "default model is required")
}

func TestNewOllamaModelClientBuildsClient(t *testing.T) {
	rt := &Runtime{}

	client, err := rt.NewOllamaModelClient(OllamaConfig{
		ServerURL:    "http://localhost:11434/",
		DefaultModel: "llama3.1",
		HighModel:    "qwen3:32b",
		SmallModel:   "llama3.2",
		MaxTokens:    1024,
		Temperature:  0.2,
	})
	require.NoError(t, err)

	ollamaClient, ok := client.(*ollamafeature.Client)
	require.True(t, ok)
	value := reflect.ValueOf(ollamaClient).Elem()
	require.Equal(t, "http://localhost:11434", value.FieldByName("serverURL").String())
	require.Equal(t, "llama3.1", value.FieldByName("defaultModel").String())
	require.Equal(t, "qwen3:32b", value.FieldByName("highModel").String())
	require.Equal(t, "llama3.2", value.FieldByName("smallModel").String())
}

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

func TestNewVertexGeminiModelClientBuildsSDKClient(t *testing.T) {
	rt := &Runtime{}

	client, err := rt.NewVertexGeminiModelClient(context.Background(), VertexConfig{
		ProjectID:      "project",
		Location:       "global",
		Credentials:    &auth.Credentials{},
		DefaultModel:   "gemini-2.5-pro",
		HighModel:      "gemini-2.5-pro",
		SmallModel:     "gemini-2.5-flash",
		MaxTokens:      2048,
		Temperature:    0.2,
		ThinkingBudget: 4096,
	})
	require.NoError(t, err)

	geminiClient, ok := client.(*geminifeature.Client)
	require.True(t, ok)
	value := reflect.ValueOf(geminiClient).Elem()
	require.Equal(t, "gemini-2.5-pro", value.FieldByName("defaultModel").String())
	require.Equal(t, "gemini-2.5-pro", value.FieldByName("highModel").String())
	require.Equal(t, "gemini-2.5-flash", value.FieldByName("smallModel").String())
}
