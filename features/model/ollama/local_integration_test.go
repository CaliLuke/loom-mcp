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

func localOllamaConfig(t *testing.T) (string, string) {
	t.Helper()
	if os.Getenv("OLLAMA_INTEGRATION") != "1" {
		t.Skip("set OLLAMA_INTEGRATION=1 to run local Ollama integration tests")
	}
	serverURL := strings.TrimRight(os.Getenv("OLLAMA_URL"), "/")
	if serverURL == "" {
		serverURL = "http://localhost:11434"
	}
	require.NoError(t, validateLocalOllamaURL(serverURL))
	modelName := strings.TrimSpace(os.Getenv("OLLAMA_MODEL"))
	if modelName != "" {
		return serverURL, modelName
	}
	selected, err := selectLocalOllamaModel(context.Background(), serverURL)
	require.NoError(t, err)
	return serverURL, selected
}

func selectLocalOllamaModel(ctx context.Context, serverURL string) (string, error) {
	if err := validateLocalOllamaURL(serverURL); err != nil {
		return "", err
	}
	// #nosec G704 -- opt-in local integration test; URL is restricted to localhost.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/api/tags", nil)
	if err != nil {
		return "", err
	}
	// #nosec G704 -- opt-in local integration test; URL is restricted to localhost.
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("ollama tags request failed")
	}
	var body struct {
		Models []struct {
			Name         string   `json:"name"`
			Capabilities []string `json:"capabilities"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	for _, candidate := range body.Models {
		if hasCapability(candidate.Capabilities, "completion") && hasCapability(candidate.Capabilities, "tools") {
			return candidate.Name, nil
		}
	}
	return "", errors.New("no local Ollama model with completion and tools capabilities found")
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

func hasCapability(capabilities []string, want string) bool {
	for _, capability := range capabilities {
		if capability == want {
			return true
		}
	}
	return false
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
