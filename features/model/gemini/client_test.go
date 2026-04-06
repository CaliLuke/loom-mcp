package gemini_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	geminimodel "github.com/CaliLuke/loom-mcp/features/model/gemini"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
)

func TestClientComplete(t *testing.T) {
	mock := &mockModelsClient{}
	client, err := geminimodel.New(geminimodel.Options{
		Client:       mock,
		DefaultModel: "gemini-2.5-flash",
	})
	require.NoError(t, err)

	mock.response = &genai.GenerateContentResponse{
		ModelVersion: "gemini-2.5-flash-001",
		Candidates: []*genai.Candidate{
			{
				FinishReason: genai.FinishReasonStop,
				Content: &genai.Content{
					Role: "model",
					Parts: []*genai.Part{
						{Text: "hi there"},
						{
							FunctionCall: &genai.FunctionCall{
								ID:   "call-1",
								Name: "lookup",
								Args: map[string]any{"query": "docs"},
							},
						},
					},
				},
			},
		},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:        10,
			ToolUsePromptTokenCount: 2,
			CandidatesTokenCount:    5,
			CachedContentTokenCount: 3,
			TotalTokenCount:         17,
		},
	}

	resp, err := client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleSystem,
				Parts: []model.Part{
					model.TextPart{Text: "be concise"},
				},
			},
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: "ping"},
					model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("png")},
				},
			},
			{
				Role: model.ConversationRoleAssistant,
				Parts: []model.Part{
					model.ToolUsePart{ID: "tool-1", Name: "lookup", Input: map[string]any{"query": "old"}},
				},
			},
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.ToolResultPart{ToolUseID: "tool-1", Content: map[string]any{"hits": 2}},
				},
			},
		},
		Temperature: 0.4,
		MaxTokens:   128,
		Tools: []*model.ToolDefinition{
			{
				Name:        "lookup",
				Description: "Search docs",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	})
	require.NoError(t, err)

	require.Len(t, resp.Content, 1)
	require.Equal(t, model.TextPart{Text: "hi there"}, resp.Content[0].Parts[0])
	require.Len(t, resp.ToolCalls, 1)
	require.Equal(t, "lookup", string(resp.ToolCalls[0].Name))
	require.Equal(t, "call-1", resp.ToolCalls[0].ID)
	require.JSONEq(t, `{"query":"docs"}`, string(resp.ToolCalls[0].Payload))
	require.Equal(t, "STOP", resp.StopReason)
	require.Equal(t, 12, resp.Usage.InputTokens)
	require.Equal(t, 5, resp.Usage.OutputTokens)
	require.Equal(t, 17, resp.Usage.TotalTokens)
	require.Equal(t, 3, resp.Usage.CacheReadTokens)
	require.Equal(t, "gemini-2.5-flash", resp.Usage.Model)

	require.Equal(t, "gemini-2.5-flash", mock.model)
	require.NotNil(t, mock.config)
	require.Equal(t, int32(128), mock.config.MaxOutputTokens)
	require.NotNil(t, mock.config.Temperature)
	require.InDelta(t, 0.4, *mock.config.Temperature, 0.0001)
	require.NotNil(t, mock.config.SystemInstruction)
	require.Len(t, mock.config.SystemInstruction.Parts, 1)
	require.Equal(t, "be concise", mock.config.SystemInstruction.Parts[0].Text)
	require.Len(t, mock.config.Tools, 1)
	require.Len(t, mock.config.Tools[0].FunctionDeclarations, 1)
	require.Equal(t, "lookup", mock.config.Tools[0].FunctionDeclarations[0].Name)
	require.Equal(t, "Search docs", mock.config.Tools[0].FunctionDeclarations[0].Description)

	require.Len(t, mock.contents, 3)
	require.Equal(t, "user", mock.contents[0].Role)
	require.Len(t, mock.contents[0].Parts, 2)
	require.Equal(t, "ping", mock.contents[0].Parts[0].Text)
	require.Equal(t, "image/png", mock.contents[0].Parts[1].InlineData.MIMEType)
	require.Equal(t, []byte("png"), mock.contents[0].Parts[1].InlineData.Data)

	require.Equal(t, "model", mock.contents[1].Role)
	require.NotNil(t, mock.contents[1].Parts[0].FunctionCall)
	require.Equal(t, "tool-1", mock.contents[1].Parts[0].FunctionCall.ID)
	require.Equal(t, "lookup", mock.contents[1].Parts[0].FunctionCall.Name)
	require.Equal(t, map[string]any{"query": "old"}, mock.contents[1].Parts[0].FunctionCall.Args)

	require.Equal(t, "user", mock.contents[2].Role)
	require.NotNil(t, mock.contents[2].Parts[0].FunctionResponse)
	require.Equal(t, "tool-1", mock.contents[2].Parts[0].FunctionResponse.ID)
	require.Equal(t, "lookup", mock.contents[2].Parts[0].FunctionResponse.Name)
	require.Equal(t, map[string]any{"output": map[string]any{"hits": float64(2)}}, normalizeJSONMap(t, mock.contents[2].Parts[0].FunctionResponse.Response))
}

func TestClientCompleteWithToolChoiceTool(t *testing.T) {
	mock := &mockModelsClient{response: &genai.GenerateContentResponse{}}
	client, err := geminimodel.New(geminimodel.Options{
		Client:       mock,
		DefaultModel: "gemini-2.5-flash",
	})
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}},
		},
		Tools: []*model.ToolDefinition{
			{Name: "lookup", Description: "Search", InputSchema: map[string]any{"type": "object"}},
		},
		ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: "lookup"},
	})
	require.NoError(t, err)
	require.NotNil(t, mock.config.ToolConfig)
	require.NotNil(t, mock.config.ToolConfig.FunctionCallingConfig)
	require.Equal(t, genai.FunctionCallingConfigModeAny, mock.config.ToolConfig.FunctionCallingConfig.Mode)
	require.Equal(t, []string{"lookup"}, mock.config.ToolConfig.FunctionCallingConfig.AllowedFunctionNames)
}

func TestClientCompleteWithToolChoiceNone(t *testing.T) {
	mock := &mockModelsClient{response: &genai.GenerateContentResponse{}}
	client, err := geminimodel.New(geminimodel.Options{
		Client:       mock,
		DefaultModel: "gemini-2.5-flash",
	})
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}},
		},
		Tools: []*model.ToolDefinition{
			{Name: "lookup", Description: "Search", InputSchema: map[string]any{"type": "object"}},
		},
		ToolChoice: &model.ToolChoice{Mode: model.ToolChoiceModeNone},
	})
	require.NoError(t, err)
	require.Equal(t, genai.FunctionCallingConfigModeNone, mock.config.ToolConfig.FunctionCallingConfig.Mode)
}

func TestClientRequiresDefaultModel(t *testing.T) {
	_, err := geminimodel.New(geminimodel.Options{Client: &mockModelsClient{}})
	require.Error(t, err)
}

type mockModelsClient struct {
	response *genai.GenerateContentResponse
	model    string
	contents []*genai.Content
	config   *genai.GenerateContentConfig
}

func (m *mockModelsClient) GenerateContent(_ context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	m.model = model
	m.contents = contents
	m.config = config
	return m.response, nil
}

func normalizeJSONMap(t *testing.T, in map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(in)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}
