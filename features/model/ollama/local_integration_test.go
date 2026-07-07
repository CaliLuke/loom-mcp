package ollama_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	ollamamodel "github.com/CaliLuke/loom-mcp/features/model/ollama"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

func TestLocalOllamaCompleteToolCall(t *testing.T) {
	serverURL, modelName := localOllamaConfig(t)
	client, err := ollamamodel.New(ollamamodel.Options{
		ServerURL:    serverURL,
		DefaultModel: modelName,
		Temperature:  0,
		Timeout:      2 * time.Minute,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := client.Complete(ctx, &model.Request{
		Messages: []*model.Message{{
			Role: model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{
				Text: "Call the lookup_weather tool with city set to Paris. Do not answer in text.",
			}},
		}},
		Tools: []*model.ToolDefinition{{
			Name:        "lookup_weather",
			Description: "Look up weather for a city.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
				"required": []string{"city"},
			},
		}},
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.ToolCalls, "expected local Ollama model %q to emit a tool call; text=%q", modelName, assistantText(resp.Content))
	require.Equal(t, tools.Ident("lookup_weather"), resp.ToolCalls[0].Name)
	require.Contains(t, strings.ToLower(string(resp.ToolCalls[0].Payload)), "paris")
}

func TestLocalOllamaStreamText(t *testing.T) {
	serverURL, modelName := localOllamaConfig(t)
	client, err := ollamamodel.New(ollamamodel.Options{
		ServerURL:    serverURL,
		DefaultModel: modelName,
		Temperature:  0,
		Timeout:      2 * time.Minute,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	streamer, err := client.Stream(ctx, &model.Request{
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "Reply exactly: loom ollama stream ok"}},
		}},
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, streamer.Close())
	}()

	var text strings.Builder
	for {
		chunk, err := streamer.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if chunk.Type != model.ChunkTypeText || chunk.Message == nil {
			continue
		}
		text.WriteString(assistantText([]model.Message{*chunk.Message}))
	}
	require.Contains(t, strings.ToLower(text.String()), "loom ollama stream ok")
}

func TestLocalOllamaStreamThinking(t *testing.T) {
	serverURL, modelName := localOllamaThinkingConfig(t)
	client, err := ollamamodel.New(ollamamodel.Options{
		ServerURL:    serverURL,
		DefaultModel: modelName,
		Temperature:  0,
		Timeout:      2 * time.Minute,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	streamer, err := client.Stream(ctx, &model.Request{
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleSystem,
				Parts: []model.Part{model.TextPart{
					Text: "<|think|>",
				}},
			},
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{model.TextPart{
					Text: "Think briefly, then answer with this exact marker: OLLAMA_THINKING_LIVE_OK",
				}},
			},
		},
		Thinking:  &model.ThinkingOptions{Enable: true},
		MaxTokens: 256,
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, streamer.Close())
	}()

	var thinking strings.Builder
	var text strings.Builder
	for {
		chunk, err := streamer.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		switch chunk.Type {
		case model.ChunkTypeThinking:
			require.NotNil(t, chunk.Message)
			part, ok := chunk.Message.Parts[0].(model.ThinkingPart)
			require.True(t, ok)
			require.NotEmpty(t, part.Text)
			require.Equal(t, chunk.Thinking, part.Text)
			thinking.WriteString(part.Text)
		case model.ChunkTypeText:
			require.NotNil(t, chunk.Message)
			text.WriteString(assistantText([]model.Message{*chunk.Message}))
		case model.ChunkTypeCompletionDelta:
			t.Fatalf("unexpected structured-output delta in plain thinking stream: %q", chunk.CompletionDelta.Delta)
		}
	}

	require.NotEmpty(t, thinking.String(), "expected local Ollama model %q to emit native thinking chunks", modelName)
	require.Contains(t, text.String(), "OLLAMA_THINKING_LIVE_OK")
	require.NotContains(t, text.String(), thinking.String(), "thinking must not be emitted as assistant text")
}

func localOllamaConfig(t *testing.T) (string, string) {
	t.Helper()
	serverURL, explicit := localOllamaBaseConfig(t)
	if explicit != "" {
		return serverURL, explicit
	}
	selected, err := selectLocalOllamaModel(context.Background(), serverURL)
	require.NoError(t, err)
	return serverURL, selected
}

func localOllamaThinkingConfig(t *testing.T) (string, string) {
	t.Helper()
	serverURL, explicit := localOllamaBaseConfig(t)
	thinkingModel := strings.TrimSpace(os.Getenv("OLLAMA_THINKING_MODEL"))
	if thinkingModel != "" {
		if err := modelHasCapabilities(context.Background(), serverURL, thinkingModel, "completion", "thinking"); err != nil {
			t.Skipf("local Ollama thinking model %q does not expose native thinking: %v", thinkingModel, err)
		}
		return serverURL, thinkingModel
	}
	if explicit != "" {
		if err := modelHasCapabilities(context.Background(), serverURL, explicit, "completion", "thinking"); err != nil {
			t.Skipf("local Ollama model %q does not expose native thinking: %v", explicit, err)
		}
		return serverURL, explicit
	}
	selected, err := selectLocalOllamaThinkingModel(context.Background(), serverURL)
	require.NoError(t, err)
	return serverURL, selected
}

func localOllamaBaseConfig(t *testing.T) (string, string) {
	t.Helper()
	if os.Getenv("OLLAMA_INTEGRATION") != "1" {
		t.Skip("set OLLAMA_INTEGRATION=1 to run local Ollama integration tests")
	}
	serverURL := strings.TrimRight(os.Getenv("OLLAMA_URL"), "/")
	if serverURL == "" {
		serverURL = "http://localhost:11434"
	}
	require.NoError(t, validateLocalOllamaURL(serverURL))
	return serverURL, strings.TrimSpace(os.Getenv("OLLAMA_MODEL"))
}

func selectLocalOllamaModel(ctx context.Context, serverURL string) (string, error) {
	models, err := localOllamaModels(ctx, serverURL)
	if err != nil {
		return "", err
	}
	for _, candidate := range models {
		if hasCapability(candidate.Capabilities, "completion") && hasCapability(candidate.Capabilities, "tools") {
			return candidate.Name, nil
		}
	}
	return "", errors.New("no local Ollama model with completion and tools capabilities found")
}

func selectLocalOllamaThinkingModel(ctx context.Context, serverURL string) (string, error) {
	models, err := localOllamaModels(ctx, serverURL)
	if err != nil {
		return "", err
	}
	for _, candidate := range models {
		if isLocalThinkingCandidate(candidate) {
			return candidate.Name, nil
		}
	}
	return "", errors.New("no local Ollama model with completion and thinking capabilities found")
}

func modelHasCapabilities(ctx context.Context, serverURL, modelName string, capabilities ...string) error {
	models, err := localOllamaModels(ctx, serverURL)
	if err != nil {
		return err
	}
	for _, candidate := range models {
		if candidate.Name != modelName && candidate.Model != modelName {
			continue
		}
		for _, capability := range capabilities {
			if !hasCapability(candidate.Capabilities, capability) {
				return errors.New("missing capability " + capability)
			}
		}
		return nil
	}
	return errors.New("model not found")
}

func localOllamaModels(ctx context.Context, serverURL string) ([]localOllamaModel, error) {
	if err := validateLocalOllamaURL(serverURL); err != nil {
		return nil, err
	}
	// #nosec G704 -- opt-in local integration test; URL is restricted to localhost.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	// #nosec G704 -- opt-in local integration test; URL is restricted to localhost.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("ollama tags request failed")
	}
	var body struct {
		Models []localOllamaModel `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Models, nil
}

func validateLocalOllamaURL(serverURL string) error {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return err
	}
	switch parsed.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return nil
	default:
		return errors.New("OLLAMA_URL must point at localhost for local integration tests")
	}
}

type localOllamaModel struct {
	Name         string   `json:"name"`
	Model        string   `json:"model"`
	Capabilities []string `json:"capabilities"`
}

func hasCapability(capabilities []string, want string) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
}

func isLocalThinkingCandidate(candidate localOllamaModel) bool {
	return hasCapability(candidate.Capabilities, "completion") && hasCapability(candidate.Capabilities, "thinking")
}

func assistantText(messages []model.Message) string {
	var out strings.Builder
	for _, message := range messages {
		for _, part := range message.Parts {
			if text, ok := part.(model.TextPart); ok {
				out.WriteString(text.Text)
			}
		}
	}
	return out.String()
}
