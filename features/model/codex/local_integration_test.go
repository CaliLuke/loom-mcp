package codex_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/features/model/codex"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestLiveCodex(t *testing.T) {
	if os.Getenv("CODEX_INTEGRATION") != "1" {
		t.Skip("set CODEX_INTEGRATION=1 to run the live Codex smoke test")
	}
	accessToken := strings.TrimSpace(os.Getenv("CODEX_ACCESS_TOKEN"))
	accountID := strings.TrimSpace(os.Getenv("CODEX_ACCOUNT_ID"))
	modelID := strings.TrimSpace(os.Getenv("CODEX_MODEL"))
	require.NotEmpty(t, accessToken)
	require.NotEmpty(t, accountID)
	require.NotEmpty(t, modelID)

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
			return codex.Credentials{
				AccessToken: accessToken,
				AccountID:   accountID,
				Residency:   strings.TrimSpace(os.Getenv("CODEX_RESIDENCY")),
			}, nil
		}),
		Transport: transport, DefaultModel: modelID,
	})
	require.NoError(t, err)

	ctx := context.Background()
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
}
