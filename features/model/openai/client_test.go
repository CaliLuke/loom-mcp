package openai_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	openaimodel "github.com/CaliLuke/loom-mcp/features/model/openai"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
)

func TestClientComplete(t *testing.T) {
	mock := &mockResponsesClient{}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	mock.response = &responses.Response{
		Status: responses.ResponseStatusCompleted,
		Model:  "gpt-4o",
		Output: []responses.ResponseOutputItemUnion{
			{
				Type: "message",
				Content: []responses.ResponseOutputMessageContentUnion{
					{Type: "output_text", Text: "hi there"},
				},
			},
			{
				Type:      "function_call",
				Name:      "lookup",
				Arguments: `{"query":"docs"}`,
				CallID:    "call-1",
			},
		},
		Usage: responses.ResponseUsage{
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  15,
			InputTokensDetails: responses.ResponseUsageInputTokensDetails{
				CachedTokens: 3,
			},
		},
	}

	resp, err := client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{
				Role: "user",
				Parts: []model.Part{
					model.TextPart{Text: "ping"},
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
		Tools: []*model.ToolDefinition{{
			Name:        "lookup",
			Description: "Search",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	require.NoError(t, err)
	require.Len(t, resp.Content, 1)
	found := false
	for _, p := range resp.Content[0].Parts {
		if tp, ok := p.(model.TextPart); ok && tp.Text == "hi there" {
			found = true
			break
		}
	}
	require.True(t, found, "expected hi there text part")
	require.Equal(t, tools.Ident("lookup"), resp.ToolCalls[0].Name)
	require.JSONEq(t, `{"query":"docs"}`, string(resp.ToolCalls[0].Payload))
	require.Equal(t, "call-1", resp.ToolCalls[0].ID)
	require.Equal(t, "completed", resp.StopReason)
	require.Equal(t, 15, resp.Usage.TotalTokens)
	require.Equal(t, 3, resp.Usage.CacheReadTokens)

	req := mock.captured
	require.Equal(t, "gpt-4o", req.Model)
	require.Len(t, req.Tools, 1)
	functionTool := req.Tools[0].OfFunction
	require.NotNil(t, functionTool)
	require.Equal(t, "lookup", functionTool.Name)
	require.Equal(t, "Search", functionTool.Description.Value)
	require.Equal(t, "object", functionTool.Parameters["type"])

	require.Len(t, req.Input.OfInputItemList, 3)
	first := req.Input.OfInputItemList[0].OfMessage
	require.NotNil(t, first)
	require.Equal(t, responses.EasyInputMessageRoleUser, first.Role)
	require.Equal(t, "ping", first.Content.OfString.Value)

	second := req.Input.OfInputItemList[1].OfFunctionCall
	require.NotNil(t, second)
	require.Equal(t, "lookup", second.Name)
	require.Equal(t, "tool-1", second.CallID)
	require.JSONEq(t, `{"query":"old"}`, second.Arguments)

	third := req.Input.OfInputItemList[2].OfFunctionCallOutput
	require.NotNil(t, third)
	require.Equal(t, "tool-1", third.CallID)
	require.JSONEq(t, `{"hits":2}`, third.Output)
}

func TestClientCompleteWithToolChoiceTool(t *testing.T) {
	mock := &mockResponsesClient{}
	client, err := openaimodel.New(openaimodel.Options{
		Client:       mock,
		DefaultModel: "gpt-4o",
	})
	require.NoError(t, err)

	mock.response = &responses.Response{}

	_, err = client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{
				Role:  model.ConversationRoleUser,
				Parts: []model.Part{model.TextPart{Text: "ping"}},
			},
		},
		Tools: []*model.ToolDefinition{
			{
				Name:        "lookup",
				Description: "Search",
				InputSchema: map[string]any{"type": "object"},
			},
		},
		ToolChoice: &model.ToolChoice{
			Mode: model.ToolChoiceModeTool,
			Name: "lookup",
		},
	})
	require.NoError(t, err)

	req := mock.captured
	require.NotNil(t, req.ToolChoice.OfFunctionTool)
	require.Equal(t, "lookup", req.ToolChoice.OfFunctionTool.Name)
}

func TestClientCompleteWithToolChoiceNone(t *testing.T) {
	mock := &mockResponsesClient{}
	client, err := openaimodel.New(openaimodel.Options{
		Client:       mock,
		DefaultModel: "gpt-4o",
	})
	require.NoError(t, err)

	mock.response = &responses.Response{}

	_, err = client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{
				Role:  model.ConversationRoleUser,
				Parts: []model.Part{model.TextPart{Text: "ping"}},
			},
		},
		Tools: []*model.ToolDefinition{
			{
				Name:        "lookup",
				Description: "Search",
				InputSchema: map[string]any{"type": "object"},
			},
		},
		ToolChoice: &model.ToolChoice{
			Mode: model.ToolChoiceModeNone,
		},
	})
	require.NoError(t, err)

	req := mock.captured
	require.Equal(t, responses.ToolChoiceOptionsNone, req.ToolChoice.OfToolChoiceMode.Value)
}

func TestClientCompleteWithToolChoiceAny(t *testing.T) {
	mock := &mockResponsesClient{}
	client, err := openaimodel.New(openaimodel.Options{
		Client:       mock,
		DefaultModel: "gpt-4o",
	})
	require.NoError(t, err)

	mock.response = &responses.Response{}

	_, err = client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{
				Role:  model.ConversationRoleUser,
				Parts: []model.Part{model.TextPart{Text: "ping"}},
			},
		},
		Tools: []*model.ToolDefinition{
			{
				Name:        "lookup",
				Description: "Search",
				InputSchema: map[string]any{"type": "object"},
			},
		},
		ToolChoice: &model.ToolChoice{
			Mode: model.ToolChoiceModeAny,
		},
	})
	require.NoError(t, err)

	req := mock.captured
	require.Equal(t, responses.ToolChoiceOptionsRequired, req.ToolChoice.OfToolChoiceMode.Value)
}

func TestClientRequiresDefaultModel(t *testing.T) {
	_, err := openaimodel.New(openaimodel.Options{Client: &mockResponsesClient{}})
	require.Error(t, err)
}

type mockResponsesClient struct {
	response *responses.Response
	captured responses.ResponseNewParams
}

func (m *mockResponsesClient) New(ctx context.Context, request responses.ResponseNewParams, _ ...option.RequestOption) (*responses.Response, error) {
	m.captured = request
	return m.response, nil
}
