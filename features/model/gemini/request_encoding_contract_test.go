package gemini_test

import (
	"encoding/json/jsontext"
	"errors"
	"testing"

	geminimodel "github.com/CaliLuke/loom-mcp/v2/features/model/gemini"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
)

func TestGeminiToolResultRepresentations(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   any
		want    any
		invalid bool
	}{
		{"nil", nil, nil, false}, {"empty raw", rawjson.Message{}, nil, false}, {"empty jsontext", jsontext.Value{}, nil, false},
		{"raw object", rawjson.Message(`{"answer":42}`), map[string]any{"answer": float64(42)}, false},
		{"jsontext array", jsontext.Value(`[1,2]`), []any{float64(1), float64(2)}, false},
		{"raw invalid", rawjson.Message(`{`), nil, true}, {"jsontext invalid", jsontext.Value(`[`), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockModelsClient{response: &genai.GenerateContentResponse{}}
			client, err := geminimodel.New(geminimodel.Options{Client: mock, DefaultModel: "test"})
			require.NoError(t, err)
			_, err = client.Complete(t.Context(), &model.Request{Messages: []*model.Message{
				{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read"}}},
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: tc.value}}},
			}})
			if tc.invalid {
				require.Error(t, err)
				assert.Empty(t, mock.model)
				return
			}
			require.NoError(t, err)
			require.Len(t, mock.contents, 2)
			assert.Equal(t, map[string]any{"output": tc.want}, mock.contents[1].Parts[0].FunctionResponse.Response)
		})
	}
}

func TestGeminiToolChoiceAndSignedHistory(t *testing.T) {
	for _, tc := range []struct {
		mode     model.ToolChoiceMode
		expected genai.FunctionCallingConfigMode
		names    []string
	}{
		{model.ToolChoiceModeAuto, genai.FunctionCallingConfigModeAuto, nil},
		{model.ToolChoiceModeAny, genai.FunctionCallingConfigModeAny, []string{"read", "write"}},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			mock := &mockModelsClient{response: &genai.GenerateContentResponse{}}
			client, err := geminimodel.New(geminimodel.Options{Client: mock, DefaultModel: "test"})
			require.NoError(t, err)
			_, err = client.Complete(t.Context(), &model.Request{
				Tools: []*model.ToolDefinition{nil, {Name: "read"}, {Name: "write"}}, ToolChoice: &model.ToolChoice{Mode: tc.mode},
				Messages: []*model.Message{
					{Role: model.ConversationRoleSystem, Parts: []model.Part{model.CitationsPart{Text: "rules"}, model.ThinkingPart{Text: "private"}, model.CacheCheckpointPart{}}},
					{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ThinkingPart{Text: "analysis", Signature: "signed"}, model.CitationsPart{Text: "answer"}}},
					{Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: model.ImageFormatJPEG, Bytes: []byte("jpeg")}, model.ImagePart{Format: model.ImageFormatWEBP, Bytes: []byte("webp")}}},
				},
			})
			require.NoError(t, err)
			assert.Equal(t, tc.expected, mock.config.ToolConfig.FunctionCallingConfig.Mode)
			assert.Equal(t, tc.names, mock.config.ToolConfig.FunctionCallingConfig.AllowedFunctionNames)
			require.Len(t, mock.config.SystemInstruction.Parts, 1)
			assert.Equal(t, "rules", mock.config.SystemInstruction.Parts[0].Text)
			assert.True(t, mock.contents[0].Parts[0].Thought)
			assert.Equal(t, []byte("signed"), mock.contents[0].Parts[0].ThoughtSignature)
			assert.Equal(t, "answer", mock.contents[0].Parts[1].Text)
			assert.Equal(t, "image/jpeg", mock.contents[1].Parts[0].InlineData.MIMEType)
			assert.Equal(t, "image/webp", mock.contents[1].Parts[1].InlineData.MIMEType)
		})
	}
}

func TestGeminiTokenCountPropagatesFailures(t *testing.T) {
	for _, failure := range []error{assert.AnError, model.ErrRateLimited, genai.APIError{Code: 429, Message: "quota"}} {
		mock := &mockModelsClient{countErr: failure}
		client, err := geminimodel.New(geminimodel.Options{Client: mock, DefaultModel: "test"})
		require.NoError(t, err)
		_, err = client.CountTokens(t.Context(), &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}})
		var api genai.APIError
		if errors.As(failure, &api) {
			var got genai.APIError
			require.ErrorAs(t, err, &got)
			assert.Equal(t, api.Code, got.Code)
			assert.Equal(t, api.Message, got.Message)
			assert.ErrorIs(t, err, model.ErrRateLimited)
		} else {
			require.ErrorIs(t, err, failure)
		}
		assert.Equal(t, "test", mock.countModel)
	}
	mock := &mockModelsClient{}
	client, err := geminimodel.New(geminimodel.Options{Client: mock, DefaultModel: "test"})
	require.NoError(t, err)
	_, err = client.CountTokens(t.Context(), &model.Request{})
	require.Error(t, err)
	assert.Empty(t, mock.countModel)
}
