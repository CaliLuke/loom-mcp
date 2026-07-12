package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/testutil"
)

func TestClientConformance(t *testing.T) {
	request := func() *model.Request {
		return &model.Request{Messages: []*model.Message{{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "hello"}},
		}}}
	}
	newClient := func(t *testing.T, stub *stubMessagesClient) *Client {
		t.Helper()
		client, err := New(stub, Options{
			DefaultModel: "claude-sonnet",
			HighModel:    "claude-opus",
			MaxTokens:    128,
		})
		require.NoError(t, err)
		return client
	}

	testutil.RunProviderConformance(t, testutil.ProviderConformanceSuite{
		Provider: "anthropic",
		OrdinaryProviderError: func(t *testing.T) {
			providerErr := errors.New("provider unavailable")
			response, err := newClient(t, &stubMessagesClient{err: providerErr}).Complete(context.Background(), request())
			require.Nil(t, response)
			require.ErrorIs(t, err, providerErr)
			require.ErrorContains(t, err, "anthropic messages.new")
		},
		RateLimit: func(t *testing.T) {
			providerErr := newAnthropicAPIError(t, http.StatusTooManyRequests)
			response, err := newClient(t, &stubMessagesClient{err: providerErr}).Complete(context.Background(), request())
			require.Nil(t, response)
			require.ErrorIs(t, err, model.ErrRateLimited)
			var apiErr *sdk.Error
			require.ErrorAs(t, err, &apiErr)
		},
		MalformedToolCall: func(t *testing.T) {
			stub := &stubMessagesClient{resp: &sdk.Message{Content: []sdk.ContentBlockUnion{{
				Type:  anthropicContentTypeToolUse,
				Name:  "lookup",
				ID:    "tool-1",
				Input: json.RawMessage("{"),
			}}}}
			response, err := newClient(t, stub).Complete(context.Background(), request())
			require.Nil(t, response)
			require.ErrorContains(t, err, "anthropic: tool call \"tool-1\" payload")
		},
		Cancellation: func(t *testing.T) {
			stub := &stubMessagesClient{newFunc: func(ctx context.Context) (*sdk.Message, error) {
				return nil, ctx.Err()
			}}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			response, err := newClient(t, stub).Complete(ctx, request())
			require.Nil(t, response)
			require.ErrorIs(t, err, context.Canceled)
		},
		StructuredOutputAndToolChoice: func(t *testing.T) {
			stub := &stubMessagesClient{resp: &sdk.Message{}}
			client := newClient(t, stub)
			req := request()
			req.StructuredOutput = &model.StructuredOutput{Name: "draft", Schema: []byte(`{"type":"object"}`)}
			_, err := client.Complete(context.Background(), req)
			require.ErrorIs(t, err, model.ErrStructuredOutputUnsupported)

			req.StructuredOutput = nil
			req.Tools = []*model.ToolDefinition{{Name: "lookup", Description: "Search", InputSchema: map[string]any{"type": "object"}}}
			req.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: "lookup"}
			_, err = client.Complete(context.Background(), req)
			require.NoError(t, err)
			require.NotNil(t, stub.lastParams.ToolChoice.OfTool)
			require.Equal(t, "lookup", stub.lastParams.ToolChoice.OfTool.Name)
		},
		UsageAccounting: func(t *testing.T) {
			stub := &stubMessagesClient{resp: &sdk.Message{
				Model: "claude-opus",
				Usage: sdk.Usage{
					InputTokens:              10,
					OutputTokens:             5,
					CacheReadInputTokens:     3,
					CacheCreationInputTokens: 4,
				},
			}}
			req := request()
			req.ModelClass = model.ModelClassHighReasoning
			response, err := newClient(t, stub).Complete(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, model.TokenUsage{
				Model:            "claude-opus",
				ModelClass:       model.ModelClassHighReasoning,
				InputTokens:      10,
				OutputTokens:     5,
				TotalTokens:      15,
				CacheReadTokens:  3,
				CacheWriteTokens: 4,
			}, response.Usage)
		},
		Streaming: testutil.ProviderStreamingConformance{
			SetupError: func(t *testing.T) {
				providerErr := errors.New("stream setup failed")
				stub := &stubMessagesClient{stream: ssestream.NewStream[sdk.MessageStreamEventUnion](&noopDecoder{}, providerErr)}
				stream, err := newClient(t, stub).Stream(context.Background(), request())
				require.Nil(t, stream)
				require.ErrorIs(t, err, providerErr)
			},
			ReceiveError: func(t *testing.T) {
				providerErr := errors.New("stream receive failed")
				stub := &stubMessagesClient{stream: ssestream.NewStream[sdk.MessageStreamEventUnion](&testDecoder{err: providerErr}, nil)}
				stream, err := newClient(t, stub).Stream(context.Background(), request())
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, stream.Close()) })
				_, err = stream.Recv()
				require.ErrorIs(t, err, providerErr)
			},
			Terminal: func(t *testing.T) {
				messageStart := mustAnthropicStreamEvent(t, `{
					"type":"message_start",
					"message":{
						"id":"msg-1",
						"type":"message",
						"role":"assistant",
						"model":"claude-opus",
						"content":[],
						"usage":{
							"input_tokens":10,
							"output_tokens":0,
							"cache_read_input_tokens":3,
							"cache_creation_input_tokens":4
						}
					}
				}`)
				messageDelta := mustAnthropicStreamEvent(t, `{
					"type":"message_delta",
					"delta":{"stop_reason":"end_turn"},
					"usage":{"output_tokens":5}
				}`)
				messageStop := mustAnthropicStreamEvent(t, `{"type":"message_stop"}`)
				stub := &stubMessagesClient{stream: ssestream.NewStream[sdk.MessageStreamEventUnion](&testDecoder{events: []ssestream.Event{
					{Type: "message_start", Data: mustJSON(messageStart)},
					{Type: "message_delta", Data: mustJSON(messageDelta)},
					{Type: "message_stop", Data: mustJSON(messageStop)},
				}}, nil)}
				req := request()
				req.ModelClass = model.ModelClassHighReasoning
				stream, err := newClient(t, stub).Stream(context.Background(), req)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, stream.Close()) })

				usage, err := stream.Recv()
				require.NoError(t, err)
				require.Equal(t, model.ChunkTypeUsage, usage.Type)
				require.Equal(t, model.ModelClassHighReasoning, usage.UsageDelta.ModelClass)
				require.Equal(t, "claude-opus", usage.UsageDelta.Model)
				stop, err := stream.Recv()
				require.NoError(t, err)
				require.Equal(t, model.ChunkTypeStop, stop.Type)
				require.Equal(t, "end_turn", stop.StopReason)
				_, err = stream.Recv()
				require.ErrorIs(t, err, io.EOF)
			},
		},
	})
}

func mustAnthropicStreamEvent(t *testing.T, raw string) sdk.MessageStreamEventUnion {
	t.Helper()
	var event sdk.MessageStreamEventUnion
	require.NoError(t, json.Unmarshal([]byte(raw), &event))
	return event
}
