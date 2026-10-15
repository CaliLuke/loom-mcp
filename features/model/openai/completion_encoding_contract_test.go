package openai_test

import (
	"testing"

	openaimodel "github.com/CaliLuke/loom-mcp/v2/features/model/openai"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIEncodesImageMediaAndExplicitGenerationControls(t *testing.T) {
	for _, tt := range []struct {
		format model.ImageFormat
		mime   string
	}{
		{model.ImageFormatJPEG, "image/jpeg"}, {model.ImageFormatWEBP, "image/webp"}, {model.ImageFormatGIF, "image/gif"},
	} {
		t.Run(tt.mime, func(t *testing.T) {
			mock := &mockResponsesClient{response: &responses.Response{}}
			client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "model"})
			require.NoError(t, err)
			_, err = client.Complete(t.Context(), &model.Request{MaxTokens: 42, ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeAuto}, Messages: []*model.Message{nil, {Role: model.ConversationRoleSystem, Parts: []model.Part{model.CitationsPart{Text: "system evidence"}}}, {Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: tt.format, Bytes: []byte{1, 2, 3}}}}}})
			require.NoError(t, err)
			assert.EqualValues(t, 42, mock.captured.MaxOutputTokens.Value)
			assert.Equal(t, responses.ToolChoiceOptionsAuto, mock.captured.ToolChoice.OfToolChoiceMode.Value)
			require.Len(t, mock.captured.Input.OfInputItemList, 2)
			assert.Equal(t, "system evidence", mock.captured.Input.OfInputItemList[0].OfMessage.Content.OfString.Value)
			message := mock.captured.Input.OfInputItemList[1].OfMessage
			require.NotNil(t, message)
			require.Len(t, message.Content.OfInputItemContentList, 1)
			assert.Equal(t, "data:"+tt.mime+";base64,AQID", message.Content.OfInputItemContentList[0].OfInputImage.ImageURL.Value)
		})
	}
}

func TestOpenAICompletionPreservesRefusalAndEncryptedReasoning(t *testing.T) {
	mock := &mockResponsesClient{response: &responses.Response{Output: []responses.ResponseOutputItemUnion{
		{Type: "reasoning", Summary: []responses.ResponseReasoningItemSummary{{Text: ""}, {Text: "reason"}}},
		{Type: "reasoning", EncryptedContent: "opaque"},
		{Type: "message", Content: []responses.ResponseOutputMessageContentUnion{{Type: "refusal", Refusal: "declined"}}},
	}}}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "model"})
	require.NoError(t, err)
	response, err := client.Complete(t.Context(), &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}})
	require.NoError(t, err)
	require.Len(t, response.Content, 3)
	assert.Equal(t, []model.Part{model.ThinkingPart{Text: "reason", Index: 1, Final: true}}, response.Content[0].Parts)
	assert.Equal(t, []model.Part{model.ThinkingPart{Redacted: []byte("opaque"), Final: true}}, response.Content[1].Parts)
	assert.Equal(t, []model.Part{model.TextPart{Text: "declined"}}, response.Content[2].Parts)
}
