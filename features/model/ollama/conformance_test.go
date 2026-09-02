package ollama_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ollamamodel "github.com/CaliLuke/loom-mcp/v2/features/model/ollama"
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
	newClient := func(t *testing.T, handler http.HandlerFunc) *ollamamodel.Client {
		t.Helper()
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		client, err := ollamamodel.New(ollamamodel.Options{
			ServerURL:    server.URL,
			DefaultModel: "llama3.1",
			HighModel:    "deepseek-r1",
		})
		require.NoError(t, err)
		return client
	}

	testutil.RunProviderConformance(t, testutil.ProviderConformanceSuite{
		Provider: "ollama",
		OrdinaryProviderError: func(t *testing.T) {
			client := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
				assert.NoError(t, json.MarshalWrite(w, map[string]any{"error": "model runner crashed"}))
			})
			response, err := client.Complete(context.Background(), request())
			require.Nil(t, response)
			require.ErrorContains(t, err, "ollama chat: provider error: model runner crashed")
		},
		RateLimit: func(t *testing.T) {
			client := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "too many requests", http.StatusTooManyRequests)
			})
			response, err := client.Complete(context.Background(), request())
			require.Nil(t, response)
			require.ErrorIs(t, err, model.ErrRateLimited)
			require.ErrorContains(t, err, "too many requests")
		},
		MalformedToolCall: func(t *testing.T) {
			client := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
				assert.NoError(t, json.MarshalWrite(w, map[string]any{
					"model": "llama3.1",
					"done":  true,
					"message": map[string]any{"tool_calls": []any{map[string]any{
						"function": map[string]any{"arguments": map[string]any{"query": "docs"}},
					}}},
				}))
			})
			response, err := client.Complete(context.Background(), request())
			require.Nil(t, response)
			require.EqualError(t, err, "ollama: tool call name is required")
		},
		Cancellation: func(t *testing.T) {
			client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
				w.WriteHeader(http.StatusRequestTimeout)
			})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			response, err := client.Complete(ctx, request())
			require.Nil(t, response)
			require.ErrorIs(t, err, context.Canceled)
		},
		StructuredOutputAndToolChoice: func(t *testing.T) {
			var captured map[string]any
			client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				assert.NoError(t, json.UnmarshalRead(r.Body, &captured))
				assert.NoError(t, json.MarshalWrite(w, map[string]any{
					"model": "llama3.1",
					"done":  true,
					"message": map[string]any{
						"role":    "assistant",
						"content": `{"answer":"ok"}`,
					},
				}))
			})
			req := request()
			req.StructuredOutput = &model.StructuredOutput{
				Name:   "draft",
				Schema: []byte(`{"type":"object","properties":{"answer":{"type":"string"}}}`),
			}
			response, err := client.Complete(context.Background(), req)
			require.NoError(t, err)
			require.Len(t, response.Content, 1)
			require.NotNil(t, captured["format"])

			req.Tools = []*model.ToolDefinition{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}}
			_, err = client.Complete(context.Background(), req)
			require.ErrorIs(t, err, model.ErrStructuredOutputUnsupported)

			req.StructuredOutput = nil
			req.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeAny}
			_, err = client.Complete(context.Background(), req)
			require.ErrorContains(t, err, "tool choice mode \"any\" is not supported")
		},
		UsageAccounting: func(t *testing.T) {
			client := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
				assert.NoError(t, json.MarshalWrite(w, map[string]any{
					"model":             "deepseek-r1",
					"done":              true,
					"prompt_eval_count": 8,
					"eval_count":        3,
				}))
			})
			req := request()
			req.ModelClass = model.ModelClassHighReasoning
			response, err := client.Complete(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, model.TokenUsage{
				Model:        "deepseek-r1",
				ModelClass:   model.ModelClassHighReasoning,
				InputTokens:  8,
				OutputTokens: 3,
				TotalTokens:  11,
			}, response.Usage)
		},
		OutputLimited: func(t *testing.T) {
			provider := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
				assert.NoError(t, json.MarshalWrite(w, map[string]any{
					"model": "llama3.1", "done": true, "done_reason": "length",
					"message": map[string]any{"role": "assistant", "content": "{"},
				}))
			})
			response, err := provider.Complete(context.Background(), request())
			require.NoError(t, err)
			require.True(t, response.OutputLimited)
			client, err := model.NewClient(provider)
			require.NoError(t, err)
			_, err = client.Complete(context.Background(), request())
			requireOutputLimitedRejection(t, err)
		},
		MultimodalInput: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
			var captured map[string]any
			client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				assert.NoError(t, json.UnmarshalRead(r.Body, &captured))
				assert.NoError(t, json.MarshalWrite(w, map[string]any{"model": "llama3.1", "done": true}))
			})
			req := request()
			req.Messages[0].Parts = append(req.Messages[0].Parts, model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("png")})
			_, err := client.Complete(context.Background(), req)
			require.NoError(t, err)
			messages, ok := captured["messages"].([]any)
			require.True(t, ok)
			message, ok := messages[0].(map[string]any)
			require.True(t, ok)
			require.Equal(t, []any{"cG5n"}, message["images"])
		}},
		TypedThinking: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
			var captured map[string]any
			client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				assert.NoError(t, json.UnmarshalRead(r.Body, &captured))
				assert.NoError(t, json.MarshalWrite(w, map[string]any{
					"model": "deepseek-r1", "done": true,
					"message": map[string]any{"role": "assistant", "thinking": "private reasoning"},
				}))
			})
			req := request()
			req.Thinking = &model.ThinkingOptions{Enable: true}
			response, err := client.Complete(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, true, captured["think"])
			require.Len(t, response.Content, 1)
			thinking, ok := response.Content[0].Parts[0].(model.ThinkingPart)
			require.True(t, ok)
			require.Equal(t, "private reasoning", thinking.Text)
		}},
		ExactTokenCounting: testutil.ProviderCapabilityConformance{Unsupported: func(t *testing.T) {
			client := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
				assert.NoError(t, json.MarshalWrite(w, map[string]any{"model": "llama3.1", "done": true}))
			})
			_, ok := any(client).(model.TokenCounter)
			require.False(t, ok)
		}},
		ToolNameRoundTrip: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
			const canonical = "catalog.lookup"
			var captured map[string]any
			client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				assert.NoError(t, json.UnmarshalRead(r.Body, &captured))
				assert.NoError(t, json.MarshalWrite(w, map[string]any{
					"model": "llama3.1", "done": true,
					"message": map[string]any{"tool_calls": []any{map[string]any{
						"id": "call-1", "function": map[string]any{"name": canonical, "arguments": map[string]any{"query": "docs"}},
					}}},
				}))
			})
			req := request()
			req.Tools = []*model.ToolDefinition{{Name: canonical, Description: "Search", InputSchema: map[string]any{"type": "object"}}}
			response, err := client.Complete(context.Background(), req)
			require.NoError(t, err)
			require.NotNil(t, captured["tools"])
			require.Len(t, response.ToolCalls, 1)
			require.Equal(t, canonical, response.ToolCalls[0].Name.String())
		}},
		Streaming: testutil.ProviderStreamingConformance{
			SetupError: func(t *testing.T) {
				client := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
				})
				stream, err := client.Stream(context.Background(), request())
				require.Nil(t, stream)
				require.ErrorContains(t, err, "ollama chat stream: status 503: unavailable")
			},
			ReceiveError: func(t *testing.T) {
				client := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
					_, err := io.WriteString(w, "{invalid\n")
					assert.NoError(t, err)
				})
				stream, err := client.Stream(context.Background(), request())
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, stream.Close()) })
				_, err = stream.Recv()
				require.ErrorContains(t, err, "ollama: decode stream chunk")
			},
			ReceiveRateLimit: testutil.ProviderCapabilityConformance{Unsupported: func(t *testing.T) {
				client := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "too many requests", http.StatusTooManyRequests)
				})
				stream, err := client.Stream(context.Background(), request())
				require.Nil(t, stream)
				require.ErrorIs(t, err, model.ErrRateLimited)
			}},
			StateMachine: func(t *testing.T) {
				client := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
					lines := []string{
						`{"model":"deepseek-r1","message":{"role":"assistant","thinking":"consider"}}`,
						`{"model":"deepseek-r1","message":{"role":"assistant","content":"answer"}}`,
						`{"model":"deepseek-r1","message":{"role":"assistant","tool_calls":[{"id":"call-1","function":{"name":"lookup","arguments":{"query":"docs"}}}]}}`,
						`{"model":"deepseek-r1","message":{"role":"assistant","content":" follows"}}`,
						`{"model":"deepseek-r1","done":true,"done_reason":"stop","prompt_eval_count":2,"eval_count":3}`,
					}
					_, err := io.WriteString(w, strings.Join(lines, "\n")+"\n")
					assert.NoError(t, err)
				})
				stream, err := client.Stream(context.Background(), request())
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, stream.Close()) })
				chunks := testutil.CollectStreamChunks(t, stream)
				require.Equal(t, []string{
					model.ChunkTypeThinking, model.ChunkTypeText, model.ChunkTypeToolCall,
					model.ChunkTypeText, model.ChunkTypeUsage, model.ChunkTypeStop,
				}, ollamaChunkTypes(chunks))
				require.Equal(t, "call-1", chunks[2].ToolCall.ID)
				require.Equal(t, "lookup", chunks[2].ToolCall.Name.String())
				require.JSONEq(t, `{"query":"docs"}`, string(chunks[2].ToolCall.Payload))
				require.Equal(t, 5, chunks[4].UsageDelta.TotalTokens)
			},
			EarlyEOF: func(t *testing.T) {
				client := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
					_, err := io.WriteString(w, `{"model":"llama3.1","message":{"role":"assistant","content":"partial"}}`+"\n")
					assert.NoError(t, err)
				})
				stream, err := client.Stream(context.Background(), request())
				require.NoError(t, err)
				t.Cleanup(func() { _ = stream.Close() })
				chunk, err := stream.Recv()
				require.NoError(t, err)
				require.Equal(t, model.ChunkTypeText, chunk.Type)
				_, err = stream.Recv()
				require.EqualError(t, err, "ollama: stream ended before done")
			},
			PartialCancel: func(t *testing.T) {
				client := newClient(t, func(w http.ResponseWriter, r *http.Request) {
					_, err := io.WriteString(w, `{"model":"llama3.1","message":{"role":"assistant","content":"partial"}}`+"\n")
					assert.NoError(t, err)
					flusher, ok := w.(http.Flusher)
					if !assert.True(t, ok) {
						return
					}
					flusher.Flush()
					<-r.Context().Done()
				})
				ctx, cancel := context.WithCancel(context.Background())
				stream, err := client.Stream(ctx, request())
				require.NoError(t, err)
				chunk, err := stream.Recv()
				require.NoError(t, err)
				require.Equal(t, model.ChunkTypeText, chunk.Type)
				cancel()
				_, err = stream.Recv()
				require.ErrorIs(t, err, context.Canceled)
				require.NoError(t, stream.Close())
			},
			CloseError: func(t *testing.T) {
				closeErr := errors.New("stream close failed")
				body := &ollamaCloseErrorBody{Reader: strings.NewReader(`{"model":"llama3.1","done":true}` + "\n"), closeErr: closeErr}
				httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
				})}
				client, err := ollamamodel.New(ollamamodel.Options{HTTPClient: httpClient, ServerURL: "http://ollama.test", DefaultModel: "llama3.1"})
				require.NoError(t, err)
				stream, err := client.Stream(context.Background(), request())
				require.NoError(t, err)
				require.ErrorIs(t, stream.Close(), closeErr)
			},
			Terminal: func(t *testing.T) {
				client := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
					_, err := io.WriteString(w, `{"model":"deepseek-r1","done":true,"done_reason":"stop","prompt_eval_count":8,"eval_count":3}`+"\n")
					assert.NoError(t, err)
				})
				req := request()
				req.ModelClass = model.ModelClassHighReasoning
				stream, err := client.Stream(context.Background(), req)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, stream.Close()) })

				usage, err := stream.Recv()
				require.NoError(t, err)
				require.Equal(t, model.ChunkTypeUsage, usage.Type)
				require.Equal(t, model.ModelClassHighReasoning, usage.UsageDelta.ModelClass)
				require.Equal(t, "deepseek-r1", usage.UsageDelta.Model)
				stop, err := stream.Recv()
				require.NoError(t, err)
				require.Equal(t, model.ChunkTypeStop, stop.Type)
				require.Equal(t, "stop", stop.StopReason)
				_, err = stream.Recv()
				require.ErrorIs(t, err, io.EOF)
			},
			OutputLimited: func(t *testing.T) {
				provider := newClient(t, func(w http.ResponseWriter, _ *http.Request) {
					_, err := io.WriteString(w, `{"model":"llama3.1","message":{"role":"assistant","content":"{"},"done":true,"done_reason":"length"}`+"\n")
					assert.NoError(t, err)
				})
				req := request()
				req.StructuredOutput = &model.StructuredOutput{Name: "result", Schema: []byte(`{"type":"object"}`)}
				stream, err := provider.Stream(context.Background(), req)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, stream.Close()) })
				chunks := testutil.CollectStreamChunks(t, stream)
				require.Equal(t, []string{model.ChunkTypeCompletionDelta, model.ChunkTypeStop}, ollamaChunkTypes(chunks))
				require.True(t, chunks[1].OutputLimited)
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type ollamaCloseErrorBody struct {
	io.Reader
	closeErr error
}

func (b *ollamaCloseErrorBody) Close() error {
	return b.closeErr
}

func ollamaChunkTypes(chunks []model.Chunk) []string {
	types := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		types = append(types, chunk.Type)
	}
	return types
}
