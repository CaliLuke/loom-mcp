package codex

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/testutil"
)

func TestClientConformance(t *testing.T) {
	newEventsClient := func(t *testing.T, events ...string) *Client {
		t.Helper()
		return newSSETestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
			assertSSERequestHeaders(t, request)
			return sseResponse(http.StatusOK, sseEvents(events...)), nil
		}))
	}

	testutil.RunProviderConformance(t, testutil.ProviderConformanceSuite{
		Provider: "codex",
		OrdinaryProviderError: func(t *testing.T) {
			client := newSSETestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
				response := sseResponse(http.StatusBadRequest, `{"error":{"code":"invalid_request","message":"secret-token account-1"}}`)
				response.Header.Set("x-request-id", "req-safe")
				return response, nil
			}))
			response, err := client.Complete(context.Background(), testRequest())
			require.Nil(t, response)
			providerErr, ok := model.AsProviderError(err)
			require.True(t, ok)
			assert.Equal(t, model.ProviderErrorKindInvalidRequest, providerErr.Kind())
			assert.Equal(t, "invalid_request", providerErr.Code())
			assert.Equal(t, "req-safe", providerErr.RequestID())
			assert.NotContains(t, err.Error(), "secret-token")
			assert.NotContains(t, err.Error(), "account-1")
		},
		RateLimit: func(t *testing.T) {
			client := newSSETestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return sseResponse(http.StatusTooManyRequests, `{"error":{"code":"rate_limit_exceeded"}}`), nil
			}))
			response, err := client.Complete(context.Background(), testRequest())
			require.Nil(t, response)
			require.ErrorIs(t, err, model.ErrRateLimited)
		},
		MalformedToolCall: func(t *testing.T) {
			client := newEventsClient(t, `{"type":"response.completed","response":{"id":"resp-1","status":"completed","output":[{"id":"call-item","type":"function_call","name":"lookup","call_id":"call-1","arguments":"{"}]}}`)
			response, err := client.Complete(context.Background(), testRequest())
			require.Nil(t, response)
			requireInvalidStreamError(t, err)
		},
		Cancellation: func(t *testing.T) {
			client := newSSETestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return nil, request.Context().Err()
			}))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			response, err := client.Complete(ctx, testRequest())
			require.Nil(t, response)
			require.ErrorIs(t, err, context.Canceled)
		},
		StructuredOutputAndToolChoice: func(t *testing.T) {
			networkCalls := 0
			client := newSSETestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
				networkCalls++
				var body map[string]any
				require.NoError(t, json.UnmarshalRead(request.Body, &body))
				assert.Equal(t, map[string]any{"type": "function", "name": "tool_one"}, body["tool_choice"])
				return sseResponse(http.StatusOK, sseEvents(emptyTerminalEvent())), nil
			}))
			request := testRequest()
			request.StructuredOutput = &model.StructuredOutput{Name: "value", Schema: []byte(`{"type":"object"}`)}
			_, err := client.Complete(context.Background(), request)
			require.ErrorIs(t, err, model.ErrStructuredOutputUnsupported)
			assert.Zero(t, networkCalls)

			request.StructuredOutput = nil
			request.Tools = []*model.ToolDefinition{{Name: "tool.one", InputSchema: map[string]any{"type": "object"}}}
			request.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: "tool.one"}
			_, err = client.Complete(context.Background(), request)
			require.NoError(t, err)
			assert.Equal(t, 1, networkCalls)
		},
		UsageAccounting: func(t *testing.T) {
			client := newEventsClient(t, `{"type":"response.completed","response":{"id":"resp-1","status":"completed","usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13,"input_tokens_details":{"cached_tokens":4}}}}`)
			request := testRequest()
			request.ModelClass = model.ModelClassDefault
			response, err := client.Complete(context.Background(), request)
			require.NoError(t, err)
			assert.Equal(t, model.TokenUsage{Model: "gpt-codex", ModelClass: model.ModelClassDefault, InputTokens: 6, OutputTokens: 3, TotalTokens: 13, CacheReadTokens: 4}, response.Usage)
		},
		OutputLimited: func(t *testing.T) {
			client := newEventsClient(t, `{"type":"response.incomplete","response":{"id":"resp-1","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`)
			response, err := client.Complete(context.Background(), testRequest())
			require.NoError(t, err)
			require.True(t, response.OutputLimited)
		},
		MultimodalInput: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
			client := newSSETestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				assert.Contains(t, string(body), "data:image/png;base64,cG5n")
				return sseResponse(http.StatusOK, sseEvents(emptyTerminalEvent())), nil
			}))
			request := testRequest()
			request.Messages[0].Parts = append(request.Messages[0].Parts, model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("png")})
			_, err := client.Complete(context.Background(), request)
			require.NoError(t, err)
		}},
		TypedThinking: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
			client := newEventsClient(t, `{"type":"response.completed","response":{"id":"resp-1","status":"completed","output":[{"id":"reason-1","type":"reasoning","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"thought"}]}]}}`)
			response, err := client.Complete(context.Background(), testRequest())
			require.NoError(t, err)
			require.Len(t, response.Content, 2)
			thinking, ok := response.Content[1].Parts[0].(model.ThinkingPart)
			require.True(t, ok)
			assert.Equal(t, []byte("opaque"), thinking.Redacted)
		}},
		ExactTokenCounting: testutil.ProviderCapabilityConformance{Unsupported: func(t *testing.T) {
			client := newEventsClient(t, emptyTerminalEvent())
			_, ok := any(client).(model.TokenCounter)
			assert.False(t, ok)
		}},
		ToolNameRoundTrip: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
			client := newEventsClient(t, `{"type":"response.completed","response":{"id":"resp-1","status":"completed","output":[{"id":"item","type":"function_call","name":"catalog_lookup","call_id":"call-1","arguments":"{\"q\":\"x\"}"}]}}`)
			request := testRequest()
			request.Tools = []*model.ToolDefinition{{Name: "catalog.lookup", InputSchema: map[string]any{"type": "object"}}}
			response, err := client.Complete(context.Background(), request)
			require.NoError(t, err)
			require.Len(t, response.ToolCalls, 1)
			assert.Equal(t, "catalog.lookup", string(response.ToolCalls[0].Name))
		}},
		Streaming: testutil.ProviderStreamingConformance{
			SetupError: func(t *testing.T) {
				client := newSSETestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("network down")
				}))
				stream, err := client.Stream(context.Background(), testRequest())
				require.Nil(t, stream)
				providerErr, ok := model.AsProviderError(err)
				require.True(t, ok)
				assert.Equal(t, model.ProviderErrorKindUnavailable, providerErr.Kind())
			},
			ReceiveError: func(t *testing.T) {
				client := newEventsClient(t, `{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`, `{`)
				stream, err := client.Stream(context.Background(), testRequest())
				require.NoError(t, err)
				_, err = stream.Recv()
				requireInvalidStreamError(t, err)
			},
			ReceiveRateLimit: testutil.ProviderCapabilityConformance{Supported: func(t *testing.T) {
				client := newEventsClient(t, `{"type":"error","error":{"code":"rate_limit_exceeded","status":429,"request_id":"req-1"}}`)
				stream, err := client.Stream(context.Background(), testRequest())
				require.NoError(t, err)
				_, err = stream.Recv()
				require.ErrorIs(t, err, model.ErrRateLimited)
			}},
			StateMachine: func(t *testing.T) {
				client := newEventsClient(t,
					`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
					`{"type":"response.output_text.delta","item_id":"msg","output_index":0,"content_index":0,"delta":"hel"}`,
					`{"type":"response.output_text.done","item_id":"msg","output_index":0,"content_index":0,"text":"hello"}`,
					`{"type":"response.output_item.added","output_index":1,"item":{"id":"reason","type":"reasoning"}}`,
					`{"type":"response.reasoning_summary_text.delta","item_id":"reason","output_index":1,"summary_index":0,"delta":"why"}`,
					`{"type":"response.reasoning_summary_text.done","item_id":"reason","output_index":1,"summary_index":0,"text":"why"}`,
					`{"type":"response.output_item.added","output_index":2,"item":{"id":"tool","type":"function_call","name":"catalog_lookup","call_id":"call-1"}}`,
					`{"type":"response.function_call_arguments.delta","item_id":"tool","output_index":2,"delta":"{\"q\":"}`,
					`{"type":"response.function_call_arguments.done","item_id":"tool","output_index":2,"arguments":"{\"q\":\"x\"}"}`,
					`{"type":"response.completed","response":{"id":"resp-1","status":"completed","output":[{"id":"msg","type":"message","content":[{"type":"output_text","text":"hello"}]},{"id":"reason","type":"reasoning","summary":[{"type":"summary_text","text":"why"}]},{"id":"tool","type":"function_call","name":"catalog_lookup","call_id":"call-1","arguments":"{\"q\":\"x\"}"}]}}`,
				)
				request := testRequest()
				request.Tools = []*model.ToolDefinition{{Name: "catalog.lookup", InputSchema: map[string]any{"type": "object"}}}
				stream, err := client.Stream(context.Background(), request)
				require.NoError(t, err)
				chunks := testutil.CollectStreamChunks(t, stream)
				assert.IsType(t, model.TextChunk{}, chunks[0])
				assert.IsType(t, model.ThinkingChunk{}, chunks[2])
				assert.IsType(t, model.ToolCallDeltaChunk{}, chunks[4])
				assert.IsType(t, model.ToolCallChunk{}, chunks[5])
				assert.IsType(t, model.StopChunk{}, chunks[len(chunks)-1])
			},
			EarlyEOF: func(t *testing.T) {
				client := newSSETestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
					return sseResponse(http.StatusOK, ""), nil
				}))
				stream, err := client.Stream(context.Background(), testRequest())
				require.NoError(t, err)
				_, err = stream.Recv()
				require.Error(t, err)
				assert.Nil(t, stream.Response())
			},
			PartialCancel: func(t *testing.T) {
				reader, writer := io.Pipe()
				client := newSSETestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: reader}, nil
				}))
				ctx, cancel := context.WithCancel(context.Background())
				stream, err := client.Stream(ctx, testRequest())
				require.NoError(t, err)
				go func() {
					_, _ = io.WriteString(writer, sseEvents(
						`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`,
						`{"type":"response.output_text.delta","item_id":"msg","output_index":0,"content_index":0,"delta":"partial"}`,
					))
				}()
				_, err = stream.Recv()
				require.NoError(t, err)
				cancel()
				_, err = stream.Recv()
				require.ErrorIs(t, err, context.Canceled)
			},
			CloseError: func(t *testing.T) {
				closeErr := errors.New("close failed")
				client := newSSETestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: &testReadCloser{Reader: strings.NewReader(sseEvents(emptyTerminalEvent())), closeErr: closeErr}}, nil
				}))
				stream, err := client.Stream(context.Background(), testRequest())
				require.NoError(t, err)
				testutil.CollectStreamChunks(t, stream)
				require.ErrorIs(t, stream.Close(), closeErr)
				require.ErrorIs(t, stream.Close(), closeErr)
			},
			Terminal: func(t *testing.T) {
				client := newEventsClient(t, textTerminalEvent("done"))
				stream, err := client.Stream(context.Background(), testRequest())
				require.NoError(t, err)
				chunks := testutil.CollectStreamChunks(t, stream)
				require.NotEmpty(t, chunks)
				response := stream.Response()
				require.NotNil(t, response)
				assert.Equal(t, "completed", response.StopReason)
			},
			OutputLimited: func(t *testing.T) {
				client := newEventsClient(t, `{"type":"response.incomplete","response":{"id":"resp-1","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`)
				stream, err := client.Stream(context.Background(), testRequest())
				require.NoError(t, err)
				testutil.CollectStreamChunks(t, stream)
				assert.True(t, stream.Response().OutputLimited)
			},
		},
	})
}

func TestSSEIdleTimeoutResetsAfterAcceptedEvents(t *testing.T) {
	const (
		idle  = time.Second
		pause = 600 * time.Millisecond
	)
	client, err := New(Options{
		CredentialSource: CredentialSourceFunc(testCredentials),
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			reader, writer := io.Pipe()
			go func() {
				defer func() { _ = writer.Close() }()
				for _, event := range []string{`{"type":"response.created","response":{"id":"resp-1","status":"in_progress"}}`, emptyTerminalEvent()} {
					time.Sleep(pause)
					if _, err := io.WriteString(writer, sseEvents(event)); err != nil {
						return
					}
				}
			}()
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: reader}, nil
		})},
		Transport: TransportSSE, DefaultModel: "model", StreamIdleTimeout: idle,
	})
	require.NoError(t, err)
	_, err = client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
}

func TestSSEParsesMultilineDataEvent(t *testing.T) {
	body := "data: {\"type\":\"response.completed\",\n" +
		"data: \"response\":{\"id\":\"resp-1\",\"status\":\"completed\"}}\n\n"
	client := newSSETestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return sseResponse(http.StatusOK, body), nil
	}))
	_, err := client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
}

func TestSSEResponsesLiteHeader(t *testing.T) {
	client, err := New(Options{
		CredentialSource: CredentialSourceFunc(testCredentials),
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			assert.Equal(t, "true", request.Header.Get(responsesLiteHeader))
			return sseResponse(http.StatusOK, sseEvents(emptyTerminalEvent())), nil
		})},
		Transport: TransportSSE, DefaultModel: "model", ResponsesLite: true,
	})
	require.NoError(t, err)
	_, err = client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
}

func TestSSERejectsDoneSentinel(t *testing.T) {
	client := newEventsClientForTimeout(t, 20*time.Millisecond, "[DONE]")
	stream, err := client.Stream(context.Background(), testRequest())
	require.NoError(t, err)
	_, err = stream.Recv()
	requireInvalidStreamError(t, err)
}

func TestSSEEventLimitCountsEntireFrame(t *testing.T) {
	newSource := func(frame string) *sseSource {
		body := io.NopCloser(strings.NewReader(frame))
		return &sseSource{ctx: context.Background(), body: body, scanner: newSSEScanner(body), idle: time.Minute}
	}
	exactLF := strings.Repeat("x", maxStreamEventBytes-len("data: ")-2)
	data, err := newSource("data: " + exactLF + "\n\n").Next()
	require.NoError(t, err)
	assert.Len(t, data, len(exactLF))

	exactCRLF := strings.Repeat("x", maxStreamEventBytes-len("data: ")-4)
	data, err = newSource("data: " + exactCRLF + "\r\n\r\n").Next()
	require.NoError(t, err)
	assert.Len(t, data, len(exactCRLF))

	oversizedIgnoredFrame := strings.Repeat(":ignored\n", maxStreamEventBytes/len(":ignored\n")+1) + "\n"
	_, err = newSource(oversizedIgnoredFrame).Next()
	require.ErrorContains(t, err, "exceeds 16 MiB")

	oversizedCRLFFrame := strings.Repeat(":\r\n", maxStreamEventBytes/len(":\r\n")+1) + "\r\n"
	_, err = newSource(oversizedCRLFFrame).Next()
	require.ErrorContains(t, err, "exceeds 16 MiB")
}

func newEventsClientForTimeout(t *testing.T, timeout time.Duration, events ...string) *Client {
	t.Helper()
	client, err := New(Options{
		CredentialSource: CredentialSourceFunc(testCredentials),
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return sseResponse(http.StatusOK, sseEvents(events...)), nil
		})},
		Transport: TransportSSE, StreamIdleTimeout: timeout, DefaultModel: "gpt-codex",
	})
	require.NoError(t, err)
	return client
}

func requireInvalidStreamError(t *testing.T, err error) {
	t.Helper()
	providerErr, ok := model.AsProviderError(err)
	require.True(t, ok)
	assert.Equal(t, model.ProviderErrorKindUnknown, providerErr.Kind())
	assert.False(t, providerErr.Retryable())
}

func assertSSERequestHeaders(t *testing.T, request *http.Request) {
	t.Helper()
	assert.Equal(t, codexResponsesURL, request.URL.String())
	assert.Equal(t, "chatgpt.com", request.URL.Host)
	assert.Equal(t, "Bearer secret-token", request.Header.Get("Authorization"))
	assert.Equal(t, "account-1", request.Header.Get("chatgpt-account-id"))
	assert.Equal(t, "us", request.Header.Get("x-openai-internal-codex-residency"))
	assert.Equal(t, sseBetaHeader, request.Header.Get("OpenAI-Beta"))
	assert.Equal(t, "identity", request.Header.Get("Accept-Encoding"))
	assert.Equal(t, "pi", request.Header.Get("originator"))
	assert.Equal(t, defaultClientVersion, request.Header.Get("version"))
	assert.Equal(t, "text/event-stream", request.Header.Get("Accept"))
	assert.Equal(t, "application/json", request.Header.Get("Content-Type"))
	assert.Equal(t, codexUserAgent, request.Header.Get("User-Agent"))
	for _, header := range []string{
		"x-api-key", "session_id", "installation", "attestation", "x-models-etag",
		"conversation_id", "x-openai-internal-codex-conversation-id",
		"x-openai-internal-codex-remote-compaction", "x-codex-credit", "content-encoding",
	} {
		assert.Empty(t, request.Header.Get(header), header)
	}
}
