package anthropic

import (
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnthropicEncodesImagesAndToolChoices(t *testing.T) {
	for _, tc := range []struct {
		format model.ImageFormat
		mime   string
	}{{model.ImageFormatJPEG, "image/jpeg"}, {model.ImageFormatWEBP, "image/webp"}, {model.ImageFormatGIF, "image/gif"}} {
		t.Run(string(tc.format), func(t *testing.T) {
			stub := &stubMessagesClient{resp: &sdk.Message{Content: []sdk.ContentBlockUnion{{Type: "text", Text: "answer"}}, StopReason: sdk.StopReasonEndTurn}}
			client, err := New(stub, Options{DefaultModel: "claude", MaxTokens: 128})
			require.NoError(t, err)
			response, err := client.Complete(t.Context(), &model.Request{Temperature: 0.5, Messages: []*model.Message{
				nil,
				{Role: model.ConversationRoleSystem, Parts: []model.Part{model.CitationsPart{Text: "policy"}}},
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{}, model.CitationsPart{}, model.ImagePart{Format: tc.format, Bytes: []byte("image")}}},
			}})
			require.NoError(t, err)
			require.Len(t, response.Content, 1)
			assert.InDelta(t, 0.5, stub.lastParams.Temperature.Value, 0.000001)
			data, err := json.Marshal(stub.lastParams)
			require.NoError(t, err)
			assert.Contains(t, string(data), tc.mime)
			assert.Contains(t, string(data), `"data":"aW1hZ2U="`)
			assert.Contains(t, string(data), `"text":"policy"`)
			require.Len(t, stub.lastParams.Messages, 1)
		})
	}
	for _, tc := range []struct {
		mode model.ToolChoiceMode
		want string
	}{
		{model.ToolChoiceModeAuto, ""},
		{model.ToolChoiceModeNone, `"tool_choice":{"type":"none"}`},
		{model.ToolChoiceModeAny, `"tool_choice":{"type":"any"}`},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			stub := &stubMessagesClient{resp: &sdk.Message{Content: []sdk.ContentBlockUnion{{Type: "text", Text: "answer"}}, StopReason: sdk.StopReasonEndTurn}}
			client, err := New(stub, Options{DefaultModel: "claude", MaxTokens: 128})
			require.NoError(t, err)
			_, err = client.Complete(t.Context(), &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}, Tools: []*model.ToolDefinition{{Name: "read", Description: "Read data", InputSchema: map[string]any{"type": "object"}}}, ToolChoice: &model.ToolChoice{Mode: tc.mode}})
			require.NoError(t, err)
			encoded, err := json.Marshal(stub.lastParams)
			require.NoError(t, err)
			if tc.want == "" {
				assert.NotContains(t, string(encoded), `"tool_choice"`)
			} else {
				assert.Contains(t, string(encoded), tc.want)
			}
		})
	}
}

func TestAnthropicCompleteDiscardsIncompleteReasoningBlocks(t *testing.T) {
	stub := &stubMessagesClient{resp: &sdk.Message{StopReason: sdk.StopReasonEndTurn, Content: []sdk.ContentBlockUnion{
		{Type: "text"}, {Type: "thinking", Thinking: "unsigned"}, {Type: "thinking", Signature: "without text"}, {Type: "redacted_thinking"},
		{Type: "text", Text: "answer"},
	}}}
	client, err := New(stub, Options{DefaultModel: "claude", MaxTokens: 128})
	require.NoError(t, err)
	response, err := client.Complete(t.Context(), &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}})
	require.NoError(t, err)
	assert.Equal(t, []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "answer"}}}}, response.Content)
}

func TestAnthropicConstructionAndMissingResponseFailExplicitly(t *testing.T) {
	client, err := New(nil, Options{DefaultModel: "claude"})
	require.ErrorContains(t, err, "client is required")
	assert.Nil(t, client)
	client, err = New(&stubMessagesClient{}, Options{})
	require.ErrorContains(t, err, "model identifier")
	assert.Nil(t, client)
	client, err = NewFromAPIKey("", "claude")
	require.ErrorContains(t, err, "api key")
	assert.Nil(t, client)
	client, err = NewFromAPIKey("unused-test-key", "claude")
	require.NoError(t, err)
	require.NotNil(t, client)
	client, err = New(&stubMessagesClient{newFunc: func(context.Context) (*sdk.Message, error) {
		return nil, nil
	}}, Options{DefaultModel: "claude", MaxTokens: 128})
	require.NoError(t, err)
	response, err := client.Complete(t.Context(), &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}})
	require.ErrorContains(t, err, "response message is nil")
	assert.Nil(t, response)
}
