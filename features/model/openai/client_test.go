package openai_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	openaimodel "github.com/CaliLuke/loom-mcp/features/model/openai"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/testutil"
	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/ssestream"
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

func TestClientCompleteResolvesModelClass(t *testing.T) {
	tests := []struct {
		name       string
		req        model.Request
		smallModel string
		wantModel  string
	}{
		{
			name: "explicit model wins",
			req: model.Request{
				Model:      "gpt-explicit",
				ModelClass: model.ModelClassHighReasoning,
			},
			smallModel: "gpt-small",
			wantModel:  "gpt-explicit",
		},
		{
			name: "high reasoning uses configured high model",
			req: model.Request{
				ModelClass: model.ModelClassHighReasoning,
			},
			smallModel: "gpt-small",
			wantModel:  "gpt-high",
		},
		{
			name: "small uses configured small model",
			req: model.Request{
				ModelClass: model.ModelClassSmall,
			},
			smallModel: "gpt-small",
			wantModel:  "gpt-small",
		},
		{
			name: "missing class model falls back to default",
			req: model.Request{
				ModelClass: model.ModelClassSmall,
			},
			wantModel: "gpt-default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockResponsesClient{response: &responses.Response{}}
			opts := openaimodel.Options{
				Client:       mock,
				DefaultModel: "gpt-default",
				HighModel:    "gpt-high",
				SmallModel:   tt.smallModel,
			}
			client, err := openaimodel.New(opts)
			require.NoError(t, err)

			req := tt.req
			req.Messages = []*model.Message{
				{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}},
			}
			_, err = client.Complete(context.Background(), &req)
			require.NoError(t, err)
			require.Equal(t, tt.wantModel, mock.captured.Model)
		})
	}
}

func TestClientCompleteOmitsZeroTemperature(t *testing.T) {
	mock := &mockResponsesClient{response: &responses.Response{}}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "ping"}},
		}},
	})
	require.NoError(t, err)
	require.False(t, mock.captured.Temperature.Valid())
}

func TestClientCompleteSendsNonZeroTemperature(t *testing.T) {
	mock := &mockResponsesClient{response: &responses.Response{}}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), &model.Request{
		Temperature: 0.3,
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "ping"}},
		}},
	})
	require.NoError(t, err)
	require.True(t, mock.captured.Temperature.Valid())
	require.InEpsilon(t, 0.3, mock.captured.Temperature.Value, 0.0001)
}

func TestClientCompleteEncodesImagePart(t *testing.T) {
	mock := &mockResponsesClient{response: &responses.Response{}}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{{
			Role: model.ConversationRoleUser,
			Parts: []model.Part{
				model.TextPart{Text: "look"},
				model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("png")},
			},
		}},
	})
	require.NoError(t, err)

	first := mock.captured.Input.OfInputItemList[0].OfMessage
	require.NotNil(t, first)
	content := first.Content.OfInputItemContentList
	require.Len(t, content, 2)
	require.Equal(t, "look", content[0].OfInputText.Text)
	require.Equal(t, responses.ResponseInputImageDetailAuto, content[1].OfInputImage.Detail)
	require.Equal(t, "data:image/png;base64,cG5n", content[1].OfInputImage.ImageURL.Value)
}

func TestClientCompleteRejectsUnsupportedDocumentPart(t *testing.T) {
	mock := &mockResponsesClient{response: &responses.Response{}}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{{
			Role: model.ConversationRoleUser,
			Parts: []model.Part{
				model.DocumentPart{Name: "spec", Format: model.DocumentFormatTXT, Text: "hello"},
			},
		}},
	})
	require.ErrorContains(t, err, "openai responses: unsupported message part model.DocumentPart")
	require.Empty(t, mock.captured.Model)
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

func TestClientCompleteSupportsStructuredOutput(t *testing.T) {
	mock := &mockResponsesClient{}
	client, err := openaimodel.New(openaimodel.Options{
		Client:       mock,
		DefaultModel: "gpt-4o",
	})
	require.NoError(t, err)
	mock.response = &responses.Response{
		Output: []responses.ResponseOutputItemUnion{{
			Type: "message",
			Content: []responses.ResponseOutputMessageContentUnion{{
				Type: "output_text",
				Text: `{"title":"ok"}`,
			}},
		}},
	}

	_, err = client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}},
		},
		StructuredOutput: &model.StructuredOutput{
			Name:   "draft",
			Schema: []byte(`{"type":"object","required":["title"],"properties":{"title":{"type":"string"}}}`),
		},
	})
	require.NoError(t, err)

	format := mock.captured.Text.Format.OfJSONSchema
	require.NotNil(t, format)
	require.Equal(t, "draft", format.Name)
	require.True(t, format.Strict.Value)
	require.Equal(t, "object", format.Schema["type"])
}

func TestClientCompleteRejectsStructuredOutputWithTools(t *testing.T) {
	mock := &mockResponsesClient{}
	client, err := openaimodel.New(openaimodel.Options{
		Client:       mock,
		DefaultModel: "gpt-4o",
	})
	require.NoError(t, err)
	mock.response = &responses.Response{}

	_, err = client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}},
		},
		Tools: []*model.ToolDefinition{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}},
		StructuredOutput: &model.StructuredOutput{
			Name:   "draft",
			Schema: []byte(`{"type":"object"}`),
		},
	})
	require.ErrorContains(t, err, "structured output cannot be combined with tools")
}

func TestClientCompleteCanonicalizesStrictToolPayload(t *testing.T) {
	mock := &mockResponsesClient{}
	client, err := openaimodel.New(openaimodel.Options{
		Client:       mock,
		DefaultModel: "gpt-4o",
	})
	require.NoError(t, err)
	mock.response = &responses.Response{
		Output: []responses.ResponseOutputItemUnion{{
			Type:      "function_call",
			Name:      "lookup",
			Arguments: `{"query":"docs","limit":null}`,
			CallID:    "call-1",
		}},
	}

	resp, err := client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}},
		},
		Tools: []*model.ToolDefinition{{
			Name:        "lookup",
			Description: "Search",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer"},
				},
				"required": []any{"query"},
			},
		}},
	})

	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	require.JSONEq(t, `{"query":"docs"}`, string(resp.ToolCalls[0].Payload))
}

func TestClientCompleteCanonicalizesStructuredOutputPayload(t *testing.T) {
	mock := &mockResponsesClient{}
	client, err := openaimodel.New(openaimodel.Options{
		Client:       mock,
		DefaultModel: "gpt-4o",
	})
	require.NoError(t, err)
	mock.response = &responses.Response{
		Output: []responses.ResponseOutputItemUnion{{
			Type: "message",
			Content: []responses.ResponseOutputMessageContentUnion{{
				Type: "output_text",
				Text: `{"title":"ok","summary":null}`,
			}},
		}},
	}

	resp, err := client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "ping"}}},
		},
		StructuredOutput: &model.StructuredOutput{
			Name: "draft",
			Schema: []byte(`{
				"type": "object",
				"properties": {
					"title": {"type": "string"},
					"summary": {"type": "string"}
				},
				"required": ["title"]
			}`),
		},
	})

	require.NoError(t, err)
	require.Len(t, resp.Content, 1)
	require.Len(t, resp.Content[0].Parts, 1)
	part, ok := resp.Content[0].Parts[0].(model.TextPart)
	require.True(t, ok)
	require.JSONEq(t, `{"title":"ok"}`, part.Text)
}

func TestClientRequiresDefaultModel(t *testing.T) {
	_, err := openaimodel.New(openaimodel.Options{Client: &mockResponsesClient{}})
	require.Error(t, err)
}

func TestClientStreamReturnsNewStreamingError(t *testing.T) {
	streamErr := errors.New("auth failed")
	decoder := &mockStreamDecoder{}
	mock := &mockResponsesClient{
		stream: ssestream.NewStream[responses.ResponseStreamEventUnion](decoder, streamErr),
	}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	streamer, err := client.Stream(context.Background(), &model.Request{
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "Ping"}},
		}},
	})
	require.Nil(t, streamer)
	require.ErrorIs(t, err, streamErr)
	require.ErrorContains(t, err, "openai responses stream")
	require.True(t, decoder.closed)
}

func TestClientCompleteReturnsRateLimitedAPIError(t *testing.T) {
	apiErr := newOpenAIRateLimitError(t)
	mock := &mockResponsesClient{err: apiErr}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	resp, err := client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "Ping"}},
		}},
	})
	require.Nil(t, resp)
	require.ErrorIs(t, err, model.ErrRateLimited)

	var got *openai.Error
	require.ErrorAs(t, err, &got)
	require.Equal(t, http.StatusTooManyRequests, got.StatusCode)
}

func TestClientStreamReturnsRateLimitedNewStreamingError(t *testing.T) {
	streamErr := newOpenAIRateLimitError(t)
	mock := &mockResponsesClient{
		stream: newMockOpenAIStreamError(streamErr),
	}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	streamer, err := client.Stream(context.Background(), &model.Request{
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "Ping"}},
		}},
	})
	require.Nil(t, streamer)
	require.ErrorIs(t, err, model.ErrRateLimited)

	var apiErr *openai.Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
}

func TestClientStreamReturnsRateLimitedRecvError(t *testing.T) {
	streamErr := newOpenAIRateLimitError(t)
	mock := &mockResponsesClient{
		stream: newMockOpenAIStreamReadError(streamErr),
	}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	streamer, err := client.Stream(context.Background(), &model.Request{
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "Ping"}},
		}},
	})
	require.NoError(t, err)

	_, err = streamer.Recv()
	require.ErrorIs(t, err, model.ErrRateLimited)

	var apiErr *openai.Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
	require.NoError(t, streamer.Close())
}

func TestClientStreamRejectsMalformedEventJSON(t *testing.T) {
	mock := &mockResponsesClient{stream: newMockOpenAIStream("{")}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	streamer, err := client.Stream(context.Background(), &model.Request{
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "Ping"}},
		}},
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, streamer.Close())
	}()

	_, err = streamer.Recv()
	require.Error(t, err)
}

func TestClientStreamRejectsEOFBeforeCompletedEvent(t *testing.T) {
	mock := &mockResponsesClient{stream: newMockOpenAIStream(
		"{\"type\":\"response.output_text.delta\",\"sequence_number\":1,\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"partial\",\"logprobs\":[]}",
	)}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	streamer, err := client.Stream(context.Background(), &model.Request{
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "Ping"}},
		}},
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, streamer.Close())
	}()

	chunk, err := streamer.Recv()
	require.NoError(t, err)
	require.Equal(t, model.ChunkTypeText, chunk.Type)
	_, err = streamer.Recv()
	require.EqualError(t, err, "openai: stream ended before response.completed")
}

func TestClientStreamEmitsTextToolCallsUsageAndStop(t *testing.T) {
	mock := &mockResponsesClient{
		stream: newMockOpenAIStream(
			`{
				"type":"response.output_text.delta",
				"sequence_number":1,
				"item_id":"msg_1",
				"output_index":0,
				"content_index":0,
				"delta":"Hel",
				"logprobs":[]
			}`,
			`{
				"type":"response.output_text.delta",
				"sequence_number":2,
				"item_id":"msg_1",
				"output_index":0,
				"content_index":0,
				"delta":"lo",
				"logprobs":[]
			}`,
			`{
				"type":"response.output_item.added",
				"sequence_number":3,
				"output_index":1,
				"item":{
					"id":"fc_1",
					"type":"function_call",
					"call_id":"call_1",
					"name":"lookup",
					"arguments":"",
					"status":"in_progress"
				}
			}`,
			`{
				"type":"response.function_call_arguments.delta",
				"sequence_number":4,
				"item_id":"fc_1",
				"output_index":1,
				"delta":"{\"query\""
			}`,
			`{
				"type":"response.function_call_arguments.delta",
				"sequence_number":5,
				"item_id":"fc_1",
				"output_index":1,
				"delta":":\"docs\"}"
			}`,
			`{
				"type":"response.completed",
				"sequence_number":6,
				"response":{
					"model":"gpt-4o",
					"status":"completed",
					"usage":{
						"input_tokens":10,
						"input_tokens_details":{"cached_tokens":0},
						"output_tokens":5,
						"output_tokens_details":{"reasoning_tokens":0},
						"total_tokens":15
					},
					"output":[
						{
							"id":"msg_1",
							"type":"message",
							"role":"assistant",
							"status":"completed",
							"content":[{"type":"output_text","text":"Hello","annotations":[],"logprobs":[]}]
						},
						{
							"id":"fc_1",
							"type":"function_call",
							"call_id":"call_1",
							"name":"lookup",
							"arguments":"{\"query\":\"docs\"}",
							"status":"completed"
						}
					]
				}
			}`,
		),
	}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	streamer, err := client.Stream(context.Background(), &model.Request{
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "Ping"}},
		}},
		Tools: []*model.ToolDefinition{{
			Name:        "lookup",
			Description: "Search",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, streamer.Close())
	}()

	chunks := testutil.CollectStreamChunks(t, streamer)
	require.Len(t, chunks, 7)
	require.Equal(t, model.ChunkTypeText, chunks[0].Type)
	require.Equal(t, "Hel", chunks[0].Message.Parts[0].(model.TextPart).Text)
	require.Equal(t, model.ChunkTypeText, chunks[1].Type)
	require.Equal(t, "lo", chunks[1].Message.Parts[0].(model.TextPart).Text)
	require.Equal(t, model.ChunkTypeToolCallDelta, chunks[2].Type)
	require.Equal(t, "call_1", chunks[2].ToolCallDelta.ID)
	require.Equal(t, tools.Ident("lookup"), chunks[2].ToolCallDelta.Name)
	require.Equal(t, model.ChunkTypeToolCallDelta, chunks[3].Type)
	require.Equal(t, model.ChunkTypeToolCall, chunks[4].Type)
	require.Equal(t, "call_1", chunks[4].ToolCall.ID)
	require.JSONEq(t, `{"query":"docs"}`, string(chunks[4].ToolCall.Payload))
	require.Equal(t, model.ChunkTypeUsage, chunks[5].Type)
	require.Equal(t, 15, chunks[5].UsageDelta.TotalTokens)
	require.Equal(t, "gpt-4o", chunks[5].UsageDelta.Model)
	require.Equal(t, model.ChunkTypeStop, chunks[6].Type)
	require.Equal(t, "completed", chunks[6].StopReason)

	meta := streamer.Metadata()
	require.NotNil(t, meta)
	usage, ok := meta["usage"].(model.TokenUsage)
	require.True(t, ok)
	require.Equal(t, 15, usage.TotalTokens)
	require.Equal(t, "gpt-4o", usage.Model)
	require.Equal(t, "gpt-4o", mock.streamCaptured.Model)
}

func TestClientStreamStructuredOutput(t *testing.T) {
	mock := &mockResponsesClient{
		stream: newMockOpenAIStream(
			`{
				"type":"response.output_text.delta",
				"sequence_number":1,
				"item_id":"msg_1",
				"output_index":0,
				"content_index":0,
				"delta":"{\"answer\":\"ok\"}",
				"logprobs":[]
			}`,
			`{
				"type":"response.completed",
				"sequence_number":2,
				"response":{
					"model":"gpt-4o",
					"status":"completed",
					"output":[
						{
							"id":"msg_1",
							"type":"message",
							"role":"assistant",
							"status":"completed",
							"content":[{"type":"output_text","text":"{\"answer\":\"ok\"}","annotations":[],"logprobs":[]}]
						}
					]
				}
			}`,
		),
	}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	streamer, err := client.Stream(context.Background(), &model.Request{
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "Ping"}},
		}},
		StructuredOutput: &model.StructuredOutput{
			Name:   "draft",
			Schema: []byte(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`),
		},
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, streamer.Close())
	}()

	chunks := testutil.CollectStreamChunks(t, streamer)
	require.Len(t, chunks, 3)
	require.Equal(t, model.ChunkTypeCompletionDelta, chunks[0].Type)
	require.Equal(t, "draft", chunks[0].CompletionDelta.Name)
	require.JSONEq(t, `{"answer":"ok"}`, chunks[0].CompletionDelta.Delta)
	require.Equal(t, model.ChunkTypeCompletion, chunks[1].Type)
	require.Equal(t, "draft", chunks[1].Completion.Name)
	require.JSONEq(t, `{"answer":"ok"}`, string(chunks[1].Completion.Payload))
	require.Equal(t, model.ChunkTypeStop, chunks[2].Type)
}

func TestClientCompleteTranslatesDottedToolNames(t *testing.T) {
	mock := &mockResponsesClient{}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	mock.response = &responses.Response{
		Status: responses.ResponseStatusCompleted,
		Model:  "gpt-4o",
		Output: []responses.ResponseOutputItemUnion{{
			Type:      "function_call",
			Name:      "toolset_lookup",
			Arguments: `{"query":"docs"}`,
			CallID:    "call-1",
		}},
	}

	resp, err := client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{
			{
				Role:  model.ConversationRoleUser,
				Parts: []model.Part{model.TextPart{Text: "ping"}},
			},
			{
				Role: model.ConversationRoleAssistant,
				Parts: []model.Part{
					model.ToolUsePart{ID: "tool-1", Name: "toolset.lookup", Input: map[string]any{"query": "old"}},
					model.ToolUsePart{ID: "tool-2", Name: "runtime.tool_unavailable", Input: map[string]any{"requested": "gone"}},
				},
			},
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.ToolResultPart{ToolUseID: "tool-1", Content: map[string]any{"hits": 2}},
					model.ToolResultPart{ToolUseID: "tool-2", Content: map[string]any{"error": "unavailable"}},
				},
			},
		},
		Tools: []*model.ToolDefinition{{
			Name:        "toolset.lookup",
			Description: "Search",
			InputSchema: map[string]any{"type": "object"},
		}},
		ToolChoice: &model.ToolChoice{
			Mode: model.ToolChoiceModeTool,
			Name: "toolset.lookup",
		},
	})
	require.NoError(t, err)

	req := mock.captured
	require.Len(t, req.Tools, 1)
	functionTool := req.Tools[0].OfFunction
	require.NotNil(t, functionTool)
	assert.Equal(t, "toolset_lookup", functionTool.Name)

	require.NotNil(t, req.ToolChoice.OfFunctionTool)
	assert.Equal(t, "toolset_lookup", req.ToolChoice.OfFunctionTool.Name)

	require.Len(t, req.Input.OfInputItemList, 5)
	replayed := req.Input.OfInputItemList[1].OfFunctionCall
	require.NotNil(t, replayed)
	assert.Equal(t, "toolset_lookup", replayed.Name)
	unmapped := req.Input.OfInputItemList[2].OfFunctionCall
	require.NotNil(t, unmapped)
	assert.Equal(t, "runtime_tool_unavailable", unmapped.Name)

	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, tools.Ident("toolset.lookup"), resp.ToolCalls[0].Name)
	assert.Equal(t, "call-1", resp.ToolCalls[0].ID)
	assert.JSONEq(t, `{"query":"docs"}`, string(resp.ToolCalls[0].Payload))
}

func TestClientCompleteRejectsCollidingToolNames(t *testing.T) {
	mock := &mockResponsesClient{}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	_, err = client.Complete(context.Background(), &model.Request{
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "ping"}},
		}},
		Tools: []*model.ToolDefinition{
			{
				Name:        "toolset.lookup",
				Description: "Search",
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "toolset_lookup",
				Description: "Search twin",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "collides")
}

func TestClientStreamTranslatesDottedToolNames(t *testing.T) {
	mock := &mockResponsesClient{
		stream: newMockOpenAIStream(
			`{
				"type":"response.output_item.added",
				"sequence_number":1,
				"output_index":0,
				"item":{
					"id":"fc_1",
					"type":"function_call",
					"call_id":"call_1",
					"name":"toolset_lookup",
					"arguments":"",
					"status":"in_progress"
				}
			}`,
			`{
				"type":"response.function_call_arguments.delta",
				"sequence_number":2,
				"item_id":"fc_1",
				"output_index":0,
				"delta":"{\"query\":\"docs\"}"
			}`,
			`{
				"type":"response.completed",
				"sequence_number":3,
				"response":{
					"model":"gpt-4o",
					"status":"completed",
					"output":[
						{
							"id":"fc_1",
							"type":"function_call",
							"call_id":"call_1",
							"name":"toolset_lookup",
							"arguments":"{\"query\":\"docs\"}",
							"status":"completed"
						}
					]
				}
			}`,
		),
	}
	client, err := openaimodel.New(openaimodel.Options{Client: mock, DefaultModel: "gpt-4o"})
	require.NoError(t, err)

	streamer, err := client.Stream(context.Background(), &model.Request{
		Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "Ping"}},
		}},
		Tools: []*model.ToolDefinition{{
			Name:        "toolset.lookup",
			Description: "Search",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	require.NoError(t, err)
	defer func() {
		require.NoError(t, streamer.Close())
	}()

	require.Len(t, mock.streamCaptured.Tools, 1)
	functionTool := mock.streamCaptured.Tools[0].OfFunction
	require.NotNil(t, functionTool)
	assert.Equal(t, "toolset_lookup", functionTool.Name)

	chunks := testutil.CollectStreamChunks(t, streamer)
	require.Len(t, chunks, 3)
	require.Equal(t, model.ChunkTypeToolCallDelta, chunks[0].Type)
	assert.Equal(t, tools.Ident("toolset.lookup"), chunks[0].ToolCallDelta.Name)
	assert.Equal(t, "call_1", chunks[0].ToolCallDelta.ID)
	require.Equal(t, model.ChunkTypeToolCall, chunks[1].Type)
	assert.Equal(t, tools.Ident("toolset.lookup"), chunks[1].ToolCall.Name)
	assert.JSONEq(t, `{"query":"docs"}`, string(chunks[1].ToolCall.Payload))
	require.Equal(t, model.ChunkTypeStop, chunks[2].Type)
}

type mockResponsesClient struct {
	response       *responses.Response
	err            error
	stream         *ssestream.Stream[responses.ResponseStreamEventUnion]
	newFunc        func(context.Context) (*responses.Response, error)
	captured       responses.ResponseNewParams
	streamCaptured responses.ResponseNewParams
}

func (m *mockResponsesClient) New(ctx context.Context, request responses.ResponseNewParams, _ ...option.RequestOption) (*responses.Response, error) {
	m.captured = request
	if m.newFunc != nil {
		return m.newFunc(ctx)
	}
	return m.response, m.err
}

func (m *mockResponsesClient) NewStreaming(ctx context.Context, request responses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[responses.ResponseStreamEventUnion] {
	m.streamCaptured = request
	return m.stream
}

type mockStreamDecoder struct {
	events []ssestream.Event
	idx    int
	err    error
	closed bool
}

func newMockOpenAIStream(raws ...string) *ssestream.Stream[responses.ResponseStreamEventUnion] {
	events := make([]ssestream.Event, 0, len(raws))
	for _, raw := range raws {
		events = append(events, ssestream.Event{Data: []byte(raw)})
	}
	return ssestream.NewStream[responses.ResponseStreamEventUnion](&mockStreamDecoder{events: events}, nil)
}

func newMockOpenAIStreamError(err error) *ssestream.Stream[responses.ResponseStreamEventUnion] {
	return ssestream.NewStream[responses.ResponseStreamEventUnion](&mockStreamDecoder{}, err)
}

func newMockOpenAIStreamReadError(err error) *ssestream.Stream[responses.ResponseStreamEventUnion] {
	return ssestream.NewStream[responses.ResponseStreamEventUnion](&mockStreamDecoder{err: err}, nil)
}

func newOpenAIRateLimitError(t *testing.T) *openai.Error {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.openai.test/v1/responses", nil)
	require.NoError(t, err)
	return &openai.Error{
		StatusCode: http.StatusTooManyRequests,
		Request:    req,
		Response: &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Request:    req,
		},
	}
}

func (d *mockStreamDecoder) Event() ssestream.Event {
	if d.idx == 0 || d.idx > len(d.events) {
		return ssestream.Event{}
	}
	return d.events[d.idx-1]
}

func (d *mockStreamDecoder) Next() bool {
	if d.idx >= len(d.events) {
		return false
	}
	d.idx++
	return true
}

func (d *mockStreamDecoder) Close() error {
	d.closed = true
	return nil
}

func (d *mockStreamDecoder) Err() error {
	return d.err
}
