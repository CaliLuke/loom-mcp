package ollama_test

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
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
		},
	})
}
