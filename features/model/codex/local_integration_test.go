package codex_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/features/model/codex"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestLiveCodex(t *testing.T) {
	if testing.Short() {
		t.Skip("the live Codex smoke test runs separately through make test-codex-live")
	}
	credentials, skip, err := liveCodexCredentials()
	require.NoError(t, err)
	if skip != "" {
		t.Skip(skip)
	}
	modelID := strings.TrimSpace(os.Getenv("CODEX_MODEL"))
	if modelID == "" {
		modelID = "gpt-5.6-terra"
	}
	t.Logf("live Codex model=%s with the default client compatibility version", modelID)

	transport := codex.TransportAuto
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CODEX_TRANSPORT"))) {
	case "", "auto":
	case "sse":
		transport = codex.TransportSSE
	case "websocket", "ws":
		transport = codex.TransportWebSocket
	default:
		t.Fatalf("unsupported CODEX_TRANSPORT %q", os.Getenv("CODEX_TRANSPORT"))
	}
	provider, err := codex.New(codex.Options{
		CredentialSource: codex.CredentialSourceFunc(func(context.Context) (codex.Credentials, error) {
			return credentials, nil
		}),
		Transport: transport, DefaultModel: modelID,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	textResponse, err := provider.Complete(ctx, &model.Request{Messages: []*model.Message{{
		Role: model.ConversationRoleUser,
		Parts: []model.Part{
			model.TextPart{Text: "Reply with exactly: codex smoke ok"},
		},
	}}})
	require.NoError(t, err)
	require.NotEmpty(t, textResponse.Content)

	request := &model.Request{
		Messages: []*model.Message{{
			Role: model.ConversationRoleUser,
			Parts: []model.Part{
				model.TextPart{Text: "Call echo once with value codex, then report its result."},
			},
		}},
		Tools: []*model.ToolDefinition{{
			Name: "smoke.echo", Description: "Return the supplied value.",
			InputSchema: map[string]any{
				"type": "object", "required": []string{"value"},
				"properties": map[string]any{"value": map[string]any{"type": "string"}},
			},
		}},
		ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: "smoke.echo"},
	}
	toolResponse, err := provider.Complete(ctx, request)
	require.NoError(t, err)
	require.Len(t, toolResponse.ToolCalls, 1)
	call := toolResponse.ToolCalls[0]
	request.Messages = append(request.Messages,
		&model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: call.ID, Name: string(call.Name), Input: call.Payload}}},
		&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: call.ID, Content: map[string]any{"value": "codex"}}}},
	)
	request.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeNone}
	finalResponse, err := provider.Complete(ctx, request)
	require.NoError(t, err)
	require.NotEmpty(t, finalResponse.Content)
	t.Run("integer catalog", func(t *testing.T) {
		testLiveCodexIntegerCatalog(t, ctx, provider)
	})
}

// liveCodexCredentials discovers credentials only for local tests. CI never reads them.
func liveCodexCredentials() (codex.Credentials, string, error) {
	for _, key := range []string{"CI", "GITHUB_ACTIONS"} {
		if value := os.Getenv(key); value != "" && value != "false" && value != "0" {
			return codex.Credentials{}, "live Codex smoke test is disabled in CI", nil
		}
	}
	mode := os.Getenv("CODEX_INTEGRATION")
	if mode == "0" {
		return codex.Credentials{}, "CODEX_INTEGRATION=0 disables the live Codex smoke test", nil
	}
	if mode != "" && mode != "1" {
		return codex.Credentials{}, "", errors.New("CODEX_INTEGRATION must be 0 or 1")
	}
	credentials := codex.Credentials{
		AccessToken: strings.TrimSpace(os.Getenv("CODEX_ACCESS_TOKEN")),
		AccountID:   strings.TrimSpace(os.Getenv("CODEX_ACCOUNT_ID")),
		Residency:   strings.TrimSpace(os.Getenv("CODEX_RESIDENCY")),
	}
	if credentials.AccessToken != "" || credentials.AccountID != "" {
		if credentials.AccessToken == "" || credentials.AccountID == "" {
			return codex.Credentials{}, "", errors.New("set both CODEX_ACCESS_TOKEN and CODEX_ACCOUNT_ID")
		}
		return credentials, "", nil
	}
	authDir := os.Getenv("CODEX_HOME")
	if authDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return codex.Credentials{}, "", errors.New("cannot locate the local Codex home directory")
		}
		authDir = filepath.Join(homeDir, ".codex")
	}
	// #nosec G304 G703 -- local test reads the auth file from the user's configured Codex directory.
	data, err := os.ReadFile(filepath.Join(authDir, "auth.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return codex.Credentials{}, "", errors.New("cannot read local Codex auth.json")
	}
	if err == nil {
		var auth struct {
			Tokens struct {
				AccessToken string `json:"access_token"`
				AccountID   string `json:"account_id"`
			} `json:"tokens"`
		}
		if err := json.Unmarshal(data, &auth); err != nil {
			return codex.Credentials{}, "", errors.New("cannot decode local Codex auth.json")
		}
		credentials.AccessToken = strings.TrimSpace(auth.Tokens.AccessToken)
		credentials.AccountID = strings.TrimSpace(auth.Tokens.AccountID)
	}
	if credentials.AccessToken == "" || credentials.AccountID == "" {
		if mode == "1" {
			return codex.Credentials{}, "", errors.New("Codex subscription credentials are required with CODEX_INTEGRATION=1")
		}
		return codex.Credentials{}, "no local Codex subscription credentials; log in to Codex to enable the live smoke test", nil
	}
	return credentials, "", nil
}
