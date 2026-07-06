package openai_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	openaimodel "github.com/CaliLuke/loom-mcp/features/model/openai"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
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

	chunks := collectStreamChunks(t, streamer)
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

	chunks := collectStreamChunks(t, streamer)
	require.Len(t, chunks, 3)
	require.Equal(t, model.ChunkTypeCompletionDelta, chunks[0].Type)
	require.Equal(t, "draft", chunks[0].CompletionDelta.Name)
	require.JSONEq(t, `{"answer":"ok"}`, chunks[0].CompletionDelta.Delta)
	require.Equal(t, model.ChunkTypeCompletion, chunks[1].Type)
	require.Equal(t, "draft", chunks[1].Completion.Name)
	require.JSONEq(t, `{"answer":"ok"}`, string(chunks[1].Completion.Payload))
	require.Equal(t, model.ChunkTypeStop, chunks[2].Type)
}

type mockResponsesClient struct {
	response       *responses.Response
	stream         *ssestream.Stream[responses.ResponseStreamEventUnion]
	captured       responses.ResponseNewParams
	streamCaptured responses.ResponseNewParams
}

func (m *mockResponsesClient) New(ctx context.Context, request responses.ResponseNewParams, _ ...option.RequestOption) (*responses.Response, error) {
	m.captured = request
	return m.response, nil
}

func (m *mockResponsesClient) NewStreaming(ctx context.Context, request responses.ResponseNewParams, _ ...option.RequestOption) *ssestream.Stream[responses.ResponseStreamEventUnion] {
	m.streamCaptured = request
	return m.stream
}

type mockStreamDecoder struct {
	events []ssestream.Event
	idx    int
}

func newMockOpenAIStream(raws ...string) *ssestream.Stream[responses.ResponseStreamEventUnion] {
	events := make([]ssestream.Event, 0, len(raws))
	for _, raw := range raws {
		events = append(events, ssestream.Event{Data: []byte(raw)})
	}
	return ssestream.NewStream[responses.ResponseStreamEventUnion](&mockStreamDecoder{events: events}, nil)
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
	return nil
}

func (d *mockStreamDecoder) Err() error {
	return nil
}

func collectStreamChunks(t *testing.T, streamer model.Streamer) []model.Chunk {
	t.Helper()
	var chunks []model.Chunk
	for {
		chunk, err := streamer.Recv()
		if errors.Is(err, io.EOF) {
			return chunks
		}
		require.NoError(t, err)
		chunks = append(chunks, chunk)
	}
}
