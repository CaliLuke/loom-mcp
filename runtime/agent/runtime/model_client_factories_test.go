package runtime

import (
	"context"
	"testing"

	"cloud.google.com/go/auth"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/features/model/codex"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
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
		HighModel:    "gpt-4.1",
		SmallModel:   "gpt-4.1-mini",
	})
	require.NoError(t, err)

	require.NotNil(t, client)
	_, ok := client.(model.TokenCounter)
	require.False(t, ok)
}

func TestNewCodexModelClientValidates(t *testing.T) {
	rt := &Runtime{}

	client, err := rt.NewCodexModelClient(CodexConfig{DefaultModel: "gpt-5.6-codex"})
	require.Error(t, err)
	require.Nil(t, client)
	require.Contains(t, err.Error(), "credential source is required")
}

func TestNewCodexModelClientBuildsClientWithoutTokenCounter(t *testing.T) {
	rt := &Runtime{}
	client, err := rt.NewCodexModelClient(CodexConfig{
		CredentialSource: codex.CredentialSourceFunc(func(context.Context) (codex.Credentials, error) {
			return codex.Credentials{AccessToken: "token", AccountID: "account"}, nil
		}),
		DefaultModel: "gpt-5.6-codex",
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	_, ok := client.(model.TokenCounter)
	require.False(t, ok)
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

	require.NotNil(t, client)
	_, ok := client.(model.TokenCounter)
	require.False(t, ok)
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

	require.NotNil(t, client)
	_, ok := client.(model.TokenCounter)
	require.True(t, ok)
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

	require.NotNil(t, client)
	_, ok := client.(model.TokenCounter)
	require.True(t, ok)
}
