package openai_test

import (
	"context"
	"errors"
	"io"
	"testing"

	openai "github.com/openai/openai-go"
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
			require.Equal(t, model.ChunkTypeThinking, chunks[0].Type)
			require.Equal(t, "reasoning delta", chunks[0].Thinking)
			require.Equal(t, model.ChunkTypeStop, chunks[1].Type)
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
			Terminal: func(t *testing.T) {
				mock := &mockResponsesClient{stream: newMockOpenAIStream(`{
					"type":"response.completed",
					"sequence_number":1,
					"response":{
						"model":"o3",
						"status":"completed",
						"usage":{
							"input_tokens":10,
							"input_tokens_details":{"cached_tokens":3},
							"output_tokens":5,
							"output_tokens_details":{"reasoning_tokens":0},
							"total_tokens":15
						},
						"output":[]
					}
				}`)}
				req := request()
				req.ModelClass = model.ModelClassHighReasoning
				stream, err := newClient(t, mock).Stream(context.Background(), req)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, stream.Close()) })

				usage, err := stream.Recv()
				require.NoError(t, err)
				require.Equal(t, model.ChunkTypeUsage, usage.Type)
				require.Equal(t, model.ModelClassHighReasoning, usage.UsageDelta.ModelClass)
				require.Equal(t, "o3", usage.UsageDelta.Model)
				stop, err := stream.Recv()
				require.NoError(t, err)
				require.Equal(t, model.ChunkTypeStop, stop.Type)
				require.Equal(t, "completed", stop.StopReason)
				_, err = stream.Recv()
				require.ErrorIs(t, err, io.EOF)
			},
		},
	})
}
