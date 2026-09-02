package openai_test

import (
	"context"
	"errors"
	"testing"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/responses"
	"github.com/stretchr/testify/require"

	openaimodel "github.com/CaliLuke/loom-mcp/v2/features/model/openai"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/testutil"
)

func TestClientConformance(t *testing.T) {
	request := func() *model.Request {
		return &model.Request{Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "hello"}},
		}}}
	}
	newClient := func(t *testing.T, mock *mockResponsesClient) *openaimodel.Client {
		t.Helper()
		client, err := openaimodel.New(openaimodel.Options{
			Client:       mock,
			DefaultModel: "gpt-4o",
			HighModel:    "o3",
		})
		require.NoError(t, err)
		return client
	}

	testutil.RunProviderConformance(t, testutil.ProviderConformanceSuite{
		Provider: "openai",
		OrdinaryProviderError: func(t *testing.T) {
			providerErr := errors.New("provider unavailable")
			response, err := newClient(t, &mockResponsesClient{err: providerErr}).Complete(context.Background(), request())
			require.Nil(t, response)
			require.ErrorIs(t, err, providerErr)
			require.ErrorContains(t, err, "openai responses")
		},
		RateLimit: func(t *testing.T) {
			providerErr := newOpenAIRateLimitError(t)
			response, err := newClient(t, &mockResponsesClient{err: providerErr}).Complete(context.Background(), request())
			require.Nil(t, response)
			require.ErrorIs(t, err, model.ErrRateLimited)
			var apiErr *openai.Error
			require.ErrorAs(t, err, &apiErr)
		},
		MalformedToolCall: func(t *testing.T) {
			mock := &mockResponsesClient{response: &responses.Response{Output: []responses.ResponseOutputItemUnion{{
				Type:      "function_call",
				Name:      "lookup",
				CallID:    "call-1",
				Arguments: "{",
			}}}}
			response, err := newClient(t, mock).Complete(context.Background(), request())
			require.Nil(t, response)
			require.ErrorContains(t, err, "openai responses: tool call \"call-1\" payload")
		},
		Cancellation: func(t *testing.T) {
			mock := &mockResponsesClient{newFunc: func(ctx context.Context) (*responses.Response, error) {
				return nil, ctx.Err()
			}}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			response, err := newClient(t, mock).Complete(ctx, request())
			require.Nil(t, response)
			require.ErrorIs(t, err, context.Canceled)
		},
		StructuredOutputAndToolChoice: func(t *testing.T) {
			mock := &mockResponsesClient{response: &responses.Response{Output: []responses.ResponseOutputItemUnion{{
				Type: "message",
				Content: []responses.ResponseOutputMessageContentUnion{{
					Type: "output_text",
					Text: `{"answer":"ok"}`,
				}},
			}}}}
			client := newClient(t, mock)
			req := request()
			req.StructuredOutput = &model.StructuredOutput{
				Name:   "draft",
				Schema: []byte(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`),
			}
			_, err := client.Complete(context.Background(), req)
			require.NoError(t, err)
			require.NotNil(t, mock.captured.Text.Format.OfJSONSchema)
			require.Equal(t, "draft", mock.captured.Text.Format.OfJSONSchema.Name)

			req.Tools = []*model.ToolDefinition{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}}
			_, err = client.Complete(context.Background(), req)
			require.ErrorContains(t, err, "structured output cannot be combined with tools")

			req.StructuredOutput = nil
			req.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeNone}
			_, err = client.Complete(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, responses.ToolChoiceOptionsNone, mock.captured.ToolChoice.OfToolChoiceMode.Value)
		},
		UsageAccounting: func(t *testing.T) {
			mock := &mockResponsesClient{response: &responses.Response{
				Model: "o3",
				Usage: responses.ResponseUsage{
					InputTokens:  10,
					OutputTokens: 5,
					TotalTokens:  15,
					InputTokensDetails: responses.ResponseUsageInputTokensDetails{
						CachedTokens: 3,
					},
				},
			}}
			req := request()
			req.ModelClass = model.ModelClassHighReasoning
			response, err := newClient(t, mock).Complete(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, model.TokenUsage{
				Model:           "o3",
				ModelClass:      model.ModelClassHighReasoning,
				InputTokens:     10,
				OutputTokens:    5,
				TotalTokens:     15,
				CacheReadTokens: 3,
			}, response.Usage)
		},
		OutputLimited: func(t *testing.T) {
			mock := &mockResponsesClient{response: &responses.Response{
				Status:            responses.ResponseStatusIncomplete,
				IncompleteDetails: responses.ResponseIncompleteDetails{Reason: "max_output_tokens"},
			}}
			provider := newClient(t, mock)
			response, err := provider.Complete(context.Background(), request())
			require.NoError(t, err)
			require.True(t, response.OutputLimited)

			client, err := model.NewClient(provider)
			require.NoError(t, err)
			_, err = client.Complete(context.Background(), request())
			requireOutputLimitedRejection(t, err)
		},
		MultimodalInput: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
			mock := &mockResponsesClient{response: &responses.Response{}}
			req := request()
			req.Messages[0].Parts = append(req.Messages[0].Parts, model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("png")})
			_, err := newClient(t, mock).Complete(context.Background(), req)
			require.NoError(t, err)
			content := mock.captured.Input.OfInputItemList[0].OfMessage.Content.OfInputItemContentList
			require.Len(t, content, 2)
			require.Equal(t, "data:image/png;base64,cG5n", content[1].OfInputImage.ImageURL.Value)
		}},
		TypedThinking: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
			mock := &mockResponsesClient{response: &responses.Response{Output: []responses.ResponseOutputItemUnion{{
				Type:    "reasoning",
				Summary: []responses.ResponseReasoningItemSummary{{Text: "private reasoning"}},
			}}}}
			req := request()
			req.Thinking = &model.ThinkingOptions{Enable: true}
			response, err := newClient(t, mock).Complete(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, "auto", string(mock.captured.Reasoning.Summary))
			require.Len(t, response.Content, 1)
			thinking, ok := response.Content[0].Parts[0].(model.ThinkingPart)
			require.True(t, ok)
			require.Equal(t, "private reasoning", thinking.Text)
			require.True(t, thinking.Final)

			streamMock := &mockResponsesClient{stream: newMockOpenAIStream(
				`{"type":"response.reasoning_summary_text.delta","sequence_number":1,"item_id":"rs_1","output_index":0,"summary_index":0,"delta":"reasoning delta"}`,
				`{"type":"response.completed","sequence_number":2,"response":{"model":"o3","status":"completed","output":[]}}`,
			)}
			stream, err := newClient(t, streamMock).Stream(context.Background(), req)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, stream.Close()) })
			chunks := testutil.CollectStreamChunks(t, stream)
			require.Len(t, chunks, 2)
			streamThinking := chunks[0].(model.ThinkingChunk).Message.Parts[0].(model.ThinkingPart)
			require.Equal(t, "reasoning delta", streamThinking.Text)
			require.IsType(t, model.StopChunk{}, chunks[1])
		}},
		ExactTokenCounting: testutil.ProviderCapabilityConformance{Unsupported: func(t *testing.T) {
			_, ok := any(newClient(t, &mockResponsesClient{})).(model.TokenCounter)
			require.False(t, ok)
		}},
		ToolNameRoundTrip: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
			const canonical = "catalog.lookup"
			mock := &mockResponsesClient{response: &responses.Response{Output: []responses.ResponseOutputItemUnion{{
				Type: "function_call", Name: "catalog_lookup", CallID: "call-1", Arguments: `{"query":"docs"}`,
			}}}}
			req := request()
			req.Tools = []*model.ToolDefinition{{Name: canonical, Description: "Search", InputSchema: map[string]any{"type": "object"}}}
			response, err := newClient(t, mock).Complete(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, "catalog_lookup", mock.captured.Tools[0].OfFunction.Name)
			require.Len(t, response.ToolCalls, 1)
			require.Equal(t, canonical, response.ToolCalls[0].Name.String())
		}},
		Streaming: testutil.ProviderStreamingConformance{
			SetupError: func(t *testing.T) {
				providerErr := errors.New("stream setup failed")
				mock := &mockResponsesClient{stream: newMockOpenAIStreamError(providerErr)}
				stream, err := newClient(t, mock).Stream(context.Background(), request())
				require.Nil(t, stream)
				require.ErrorIs(t, err, providerErr)
			},
			ReceiveError: func(t *testing.T) {
				providerErr := errors.New("stream receive failed")
				mock := &mockResponsesClient{stream: newMockOpenAIStreamReadError(providerErr)}
				stream, err := newClient(t, mock).Stream(context.Background(), request())
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, stream.Close()) })
				_, err = stream.Recv()
				require.ErrorIs(t, err, providerErr)
			},
			ReceiveRateLimit: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
				providerErr := newOpenAIRateLimitError(t)
				mock := &mockResponsesClient{stream: newMockOpenAIStreamReadError(providerErr)}
				stream, err := newClient(t, mock).Stream(context.Background(), request())
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, stream.Close()) })
				_, err = stream.Recv()
				require.ErrorIs(t, err, model.ErrRateLimited)
				var apiErr *openai.Error
				require.ErrorAs(t, err, &apiErr)
			}},
			StateMachine: func(t *testing.T) {
				mock := &mockResponsesClient{stream: newMockOpenAIStream(openAIConformanceLifecycle()...)}
				stream, err := newClient(t, mock).Stream(context.Background(), request())
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, stream.Close()) })
				chunks := testutil.CollectStreamChunks(t, stream)
				require.Equal(t, []string{
					model.ChunkTypeThinking, model.ChunkTypeText, model.ChunkTypeText,
					model.ChunkTypeToolCallDelta, model.ChunkTypeToolCallDelta,
					model.ChunkTypeToolCall, model.ChunkTypeUsage, model.ChunkTypeStop,
				}, openAIChunkTypes(chunks))
				require.Equal(t, int64(1), chunks[1].(model.TextChunk).Message.Meta["output_index"])
				require.Equal(t, `{"query":`, chunks[3].(model.ToolCallDeltaChunk).Delta.Delta)
				for _, chunk := range chunks[3:5] {
					delta := chunk.(model.ToolCallDeltaChunk).Delta
					require.Equal(t, "call_1", delta.ID)
					require.Equal(t, "lookup", delta.Name.String())
				}
				call := chunks[5].(model.ToolCallChunk).ToolCall
				require.Equal(t, "call_1", call.ID)
				require.Equal(t, "lookup", call.Name.String())
				require.JSONEq(t, `{"query":"docs"}`, string(call.Payload))
			},
			EarlyEOF: func(t *testing.T) {
				lifecycle := openAIConformanceLifecycle()
				mock := &mockResponsesClient{stream: newMockOpenAIStream(lifecycle[:11]...)}
				stream, err := newClient(t, mock).Stream(context.Background(), request())
				require.NoError(t, err)
				t.Cleanup(func() { _ = stream.Close() })
				thinking, err := stream.Recv()
				require.NoError(t, err)
				require.IsType(t, model.ThinkingChunk{}, thinking)
				chunk, err := stream.Recv()
				require.NoError(t, err)
				require.IsType(t, model.TextChunk{}, chunk)
				_, err = stream.Recv()
				require.EqualError(t, err, "openai: stream ended before response.completed")
			},
			PartialCancel: func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				lifecycle := openAIConformanceLifecycle()
				mock := &mockResponsesClient{stream: newMockOpenAIPartialStream(ctx, lifecycle[:11]...)}
				stream, err := newClient(t, mock).Stream(ctx, request())
				require.NoError(t, err)
				thinking, err := stream.Recv()
				require.NoError(t, err)
				require.IsType(t, model.ThinkingChunk{}, thinking)
				chunk, err := stream.Recv()
				require.NoError(t, err)
				require.IsType(t, model.TextChunk{}, chunk)
				cancel()
				_, err = stream.Recv()
				require.ErrorIs(t, err, context.Canceled)
				require.NoError(t, stream.Close())
			},
			CloseError: func(t *testing.T) {
				closeErr := errors.New("stream close failed")
				decoder := &mockStreamDecoder{
					events:   openAIStreamEvents(openAIConformanceLifecycle()),
					closeErr: closeErr, closeErrOnce: true,
				}
				mock := &mockResponsesClient{stream: ssestream.NewStream[responses.ResponseStreamEventUnion](decoder, nil)}
				stream, err := newClient(t, mock).Stream(context.Background(), request())
				require.NoError(t, err)
				chunks := testutil.CollectStreamChunks(t, stream)
				require.IsType(t, model.StopChunk{}, chunks[len(chunks)-1])
				require.ErrorIs(t, stream.Close(), closeErr)
				require.Equal(t, int32(1), decoder.closeCalls.Load())
			},
			Terminal: func(t *testing.T) {
				mock := &mockResponsesClient{stream: newMockOpenAIStream(openAIConformanceLifecycle()...)}
				req := request()
				req.ModelClass = model.ModelClassHighReasoning
				stream, err := newClient(t, mock).Stream(context.Background(), req)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, stream.Close()) })
				chunks := testutil.CollectStreamChunks(t, stream)
				require.GreaterOrEqual(t, len(chunks), 2)
				usage := chunks[len(chunks)-2]
				usageValue := usage.(model.UsageChunk).Usage
				require.Equal(t, model.ModelClassHighReasoning, usageValue.ModelClass)
				require.Equal(t, "o3", usageValue.Model)
				stop := chunks[len(chunks)-1]
				require.Equal(t, "completed", stop.(model.StopChunk).Reason)
			},
			OutputLimited: func(t *testing.T) {
				raw := `{"type":"response.incomplete","sequence_number":1,"response":{"id":"resp_1","object":"response","model":"o3","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"{","annotations":[],"logprobs":[]}],"status":"incomplete"}]}}`
				mock := &mockResponsesClient{stream: newMockOpenAIStream(raw)}
				req := request()
				req.StructuredOutput = &model.StructuredOutput{Name: "result", Schema: []byte(`{"type":"object"}`)}
				stream, err := newClient(t, mock).Stream(context.Background(), req)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, stream.Close()) })
				chunks := testutil.CollectStreamChunks(t, stream)
				require.Len(t, chunks, 1)
				require.True(t, chunks[0].(model.StopChunk).OutputLimited)
			},
		},
	})
}

func requireOutputLimitedRejection(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var validationErr *model.OutputValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, model.OutputValidationOutputBounds, validationErr.Kind())
}

func openAIConformanceLifecycle() []string {
	return []string{
		`{"type":"response.created","sequence_number":1,"response":{"id":"resp_1","object":"response","model":"o3","status":"queued","output":[]}}`,
		`{"type":"response.in_progress","sequence_number":2,"response":{"id":"resp_1","object":"response","model":"o3","status":"in_progress","output":[]}}`,
		`{"type":"response.output_item.added","sequence_number":3,"output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[],"status":"in_progress"}}`,
		`{"type":"response.reasoning_summary_part.added","sequence_number":4,"item_id":"rs_1","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":""}}`,
		`{"type":"response.reasoning_summary_text.delta","sequence_number":5,"item_id":"rs_1","output_index":0,"summary_index":0,"delta":"consider"}`,
		`{"type":"response.reasoning_summary_text.done","sequence_number":6,"item_id":"rs_1","output_index":0,"summary_index":0,"text":"consider"}`,
		`{"type":"response.reasoning_summary_part.done","sequence_number":7,"item_id":"rs_1","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":"consider"}}`,
		`{"type":"response.output_item.done","sequence_number":8,"output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"consider"}],"status":"completed"}}`,
		`{"type":"response.output_item.added","sequence_number":9,"output_index":1,"item":{"id":"msg_1","type":"message","role":"assistant","content":[],"status":"in_progress"}}`,
		`{"type":"response.content_part.added","sequence_number":10,"item_id":"msg_1","output_index":1,"content_index":0,"part":{"type":"output_text","text":"","annotations":[],"logprobs":[]}}`,
		`{"type":"response.output_text.delta","sequence_number":11,"item_id":"msg_1","output_index":1,"content_index":0,"delta":"answer","logprobs":[]}`,
		`{"type":"response.output_text.delta","sequence_number":12,"item_id":"msg_1","output_index":1,"content_index":0,"delta":" follows","logprobs":[]}`,
		`{"type":"response.output_text.done","sequence_number":13,"item_id":"msg_1","output_index":1,"content_index":0,"text":"answer follows","logprobs":[]}`,
		`{"type":"response.content_part.done","sequence_number":14,"item_id":"msg_1","output_index":1,"content_index":0,"part":{"type":"output_text","text":"answer follows","annotations":[],"logprobs":[]}}`,
		`{"type":"response.output_item.done","sequence_number":15,"output_index":1,"item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"answer follows","annotations":[],"logprobs":[]}],"status":"completed"}}`,
		`{"type":"response.output_item.added","sequence_number":16,"output_index":2,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"","status":"in_progress"}}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":17,"item_id":"fc_1","output_index":2,"delta":"{\"query\":"}`,
		`{"type":"response.function_call_arguments.delta","sequence_number":18,"item_id":"fc_1","output_index":2,"delta":"\"docs\"}"}`,
		`{"type":"response.function_call_arguments.done","sequence_number":19,"item_id":"fc_1","output_index":2,"arguments":"{\"query\":\"docs\"}"}`,
		`{"type":"response.output_item.done","sequence_number":20,"output_index":2,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"query\":\"docs\"}","status":"completed"}}`,
		`{"type":"response.completed","sequence_number":21,"response":{"id":"resp_1","object":"response","model":"o3","status":"completed","usage":{"input_tokens":2,"input_tokens_details":{"cached_tokens":0},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":1},"total_tokens":5},"output":[{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"consider"}],"status":"completed"},{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"answer follows","annotations":[],"logprobs":[]}],"status":"completed"},{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"query\":\"docs\"}","status":"completed"}]}}`,
	}
}

func openAIStreamEvents(raws []string) []ssestream.Event {
	events := make([]ssestream.Event, 0, len(raws))
	for _, raw := range raws {
		events = append(events, ssestream.Event{Data: []byte(raw)})
	}
	return events
}

func openAIChunkTypes(chunks []model.Chunk) []string {
	types := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		types = append(types, chunk.Kind())
	}
	return types
}
