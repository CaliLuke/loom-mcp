package assistantapi

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
	sdkjsonrpc "github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedSDKServerElicitsDuringToolCalls(t *testing.T) {
	tests := []struct {
		name        string
		action      string
		content     map[string]any
		wantSummary string
	}{
		{
			name:        "accept",
			action:      "accept",
			content:     map[string]any{"summary": "The user supplied a better summary."},
			wantSummary: "The user supplied a better summary.",
		},
		{
			name:        "decline",
			action:      "decline",
			wantSummary: "Elicitation declined.",
		},
		{
			name:        "cancel",
			action:      "cancel",
			wantSummary: "Elicitation declined.",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			generatedServer, sdkHTTPServer := newGeneratedSDKServer(t)
			defer sdkHTTPServer.Close()

			requests := make(chan *sdkmcp.ElicitRequest, 1)
			session := connectInMemoryElicitationClient(t, generatedServer.Server, func(_ context.Context, req *sdkmcp.ElicitRequest) (*sdkmcp.ElicitResult, error) {
				requests <- req
				return &sdkmcp.ElicitResult{Action: test.action, Content: test.content}, nil
			}, nil)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
				Name: "summarize_text",
				Arguments: map[string]any{
					"text": "needs-elicitation",
				},
			})
			require.NoError(t, err)
			assert.Equal(t, map[string]any{"summary": test.wantSummary}, result.StructuredContent)

			select {
			case req := <-requests:
				require.Equal(t, "form", req.Params.Mode)
				require.Equal(t, "Provide a summary for the requested text.", req.Params.Message)
				require.NotNil(t, req.Params.RequestedSchema)
			default:
				t.Fatal("expected generated SDK server to return an elicitation/create input request")
			}
		})
	}
}

func TestGeneratedSDKServerSupportsMultiStepElicitation(t *testing.T) {
	service := &assistantsrvc{}
	corsPolicy := testRuntimeCORSPolicy(t)
	generatedServer, err := mcpassistant.NewSDKServer(service, &mcpassistant.SDKServerOptions{
		PromptProvider:  promptProvider{},
		RequestStateKey: []byte(testRequestStateKey),
		RuntimeCORS:     &corsPolicy,
	})
	require.NoError(t, err)

	var requestsMu sync.Mutex
	var messages []string
	session := connectInMemoryElicitationClient(t, generatedServer.Server, func(_ context.Context, req *sdkmcp.ElicitRequest) (*sdkmcp.ElicitResult, error) {
		requestsMu.Lock()
		messages = append(messages, req.Params.Message)
		requestsMu.Unlock()
		switch req.Params.Message {
		case "Provide a summary for the requested text.":
			return &sdkmcp.ElicitResult{Action: "accept", Content: map[string]any{"summary": "Protocol migration complete"}}, nil
		case "Who is the summary for?":
			return &sdkmcp.ElicitResult{Action: "accept", Content: map[string]any{"audience": "SDK maintainers"}}, nil
		default:
			t.Fatalf("unexpected elicitation message %q", req.Params.Message)
			return nil, nil
		}
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "summarize_text",
		Arguments: map[string]any{
			"text": "needs-multi-elicitation",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"summary": "Protocol migration complete for SDK maintainers.",
	}, result.StructuredContent)
	assert.Equal(t, []string{
		"Provide a summary for the requested text.",
		"Who is the summary for?",
	}, messages)
	assert.Equal(t, int64(3), service.summarizeCalls.Load(), "the generated handler must re-enter service code once per MRTR retry")
}

func TestGeneratedSDKServerElicitsDuringPromptAndResourceCalls(t *testing.T) {
	generatedServer, sdkHTTPServer := newGeneratedSDKServer(t)
	defer sdkHTTPServer.Close()

	var requestsMu sync.Mutex
	var messages []string
	session := connectInMemoryElicitationClient(t, generatedServer.Server, func(_ context.Context, req *sdkmcp.ElicitRequest) (*sdkmcp.ElicitResult, error) {
		requestsMu.Lock()
		messages = append(messages, req.Params.Message)
		requestsMu.Unlock()
		switch req.Params.Message {
		case "Provide prompt guidance.":
			return &sdkmcp.ElicitResult{Action: "accept", Content: map[string]any{"guidance": "Keep it protocol-focused."}}, nil
		case "Provide resource context.":
			return &sdkmcp.ElicitResult{Action: "accept", Content: map[string]any{"context": "Portable request state."}}, nil
		default:
			t.Fatalf("unexpected elicitation message %q", req.Params.Message)
			return nil, nil
		}
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prompt, err := session.GetPrompt(ctx, &sdkmcp.GetPromptParams{
		Name: "contextual_prompts",
		Arguments: map[string]string{
			"context": "needs-elicitation",
			"task":    "protocol-review",
		},
	})
	require.NoError(t, err)
	require.Len(t, prompt.Messages, 1)
	promptText, ok := prompt.Messages[0].Content.(*sdkmcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "Keep it protocol-focused.", promptText.Text)

	resource, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: "elicitation://context"})
	require.NoError(t, err)
	require.Len(t, resource.Contents, 1)
	var resourceBody map[string]any
	require.NoError(t, json.Unmarshal([]byte(resource.Contents[0].Text), &resourceBody))
	assert.Equal(t, map[string]any{"context": "Portable request state."}, resourceBody)

	assert.Equal(t, []string{"Provide prompt guidance.", "Provide resource context."}, messages)
}

func TestGeneratedSDKServerMultiStepElicitationOverStreamableHTTP(t *testing.T) {
	_, sdkHTTPServer := newGeneratedStatelessSDKServer(t)
	defer sdkHTTPServer.Close()

	recorder := &mcpRequestRecorder{base: sdkHTTPServer.Client().Transport}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "fixture-http-elicitation-client",
		Version: "1.0.0",
	}, &sdkmcp.ClientOptions{
		ElicitationHandler: multiStepElicitationHandler(t),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint: sdkHTTPServer.URL + "/rpc",
		HTTPClient: &http.Client{
			Transport: recorder,
			Timeout:   10 * time.Second,
		},
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, session.Close()) }()

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "summarize_text",
		Arguments: map[string]any{
			"text": "needs-multi-elicitation",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"summary": "Protocol migration complete for SDK maintainers.",
	}, result.StructuredContent)

	calls := recorder.methodCalls("tools/call")
	require.Len(t, calls, 3, "each input-required result must cause a separate HTTP retry")
	for _, call := range calls {
		assert.Empty(t, call.sessionID, "MCP 2026-07-28 streamable HTTP is stateless and sessionless")
	}
}

func TestGeneratedSDKServerRejectsFutureInputResponseOverStreamableHTTP(t *testing.T) {
	_, sdkHTTPServer := newGeneratedStatelessSDKServer(t)
	defer sdkHTTPServer.Close()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "fixture-http-invalid-input-client",
		Version: "1.0.0",
	}, &sdkmcp.ClientOptions{
		MultiRoundTrip: &sdkmcp.MultiRoundTripOptions{Disabled: true},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint:             sdkHTTPServer.URL + "/rpc",
		HTTPClient:           sdkHTTPServer.Client(),
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, session.Close()) }()

	params := &sdkmcp.CallToolParams{
		Name: "summarize_text",
		Arguments: map[string]any{
			"text": "needs-multi-elicitation",
		},
	}
	result, err := session.CallTool(ctx, params)
	require.NoError(t, err)
	require.True(t, result.NeedsInput())
	require.Contains(t, result.InputRequests, "loom-input-0")
	require.NotEmpty(t, result.RequestState)

	params.InputResponses = sdkmcp.InputResponseMap{
		"loom-input-0": &sdkmcp.ElicitResult{
			Action:  "accept",
			Content: map[string]any{"summary": "first response"},
		},
		"loom-input-1": &sdkmcp.ElicitResult{
			Action:  "accept",
			Content: map[string]any{"audience": "future response"},
		},
	}
	params.RequestState = result.RequestState
	_, err = session.CallTool(ctx, params)
	require.ErrorContains(t, err, "does not match pending request count")
}

func TestGeneratedSDKServerLegacyClientCompletesSingleStepElicitation(t *testing.T) {
	generatedServer, sdkHTTPServer := newGeneratedSDKServer(t)
	defer sdkHTTPServer.Close()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "fixture-legacy-elicitation-client",
		Version: "1.0.0",
	}, &sdkmcp.ClientOptions{
		ElicitationHandler: func(context.Context, *sdkmcp.ElicitRequest) (*sdkmcp.ElicitResult, error) {
			return &sdkmcp.ElicitResult{
				Action:  "accept",
				Content: map[string]any{"summary": "Legacy single-step response."},
			}, nil
		},
	})
	client.AddSendingMiddleware(func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
			if method == "server/discover" {
				return nil, &sdkjsonrpc.Error{
					Code:    sdkjsonrpc.CodeMethodNotFound,
					Message: "legacy client does not support server/discover",
				}
			}
			return next(ctx, method, req)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := generatedServer.Server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, serverSession.Close()) }()
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, session.Close()) }()
	require.Equal(t, "2025-11-25", session.InitializeResult().ProtocolVersion)

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "summarize_text",
		Arguments: map[string]any{
			"text": "needs-elicitation",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"summary": "Legacy single-step response."}, result.StructuredContent)
}

func connectInMemoryElicitationClient(
	t *testing.T,
	server *sdkmcp.Server,
	handler func(context.Context, *sdkmcp.ElicitRequest) (*sdkmcp.ElicitResult, error),
	configure func(*sdkmcp.Client),
) *sdkmcp.ClientSession {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serverSession.Close()) })

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "fixture-sdk-client",
		Version: "1.0.0",
	}, &sdkmcp.ClientOptions{ElicitationHandler: handler})
	if configure != nil {
		configure(client)
	}
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	return session
}

func multiStepElicitationHandler(t *testing.T) func(context.Context, *sdkmcp.ElicitRequest) (*sdkmcp.ElicitResult, error) {
	t.Helper()
	return func(_ context.Context, req *sdkmcp.ElicitRequest) (*sdkmcp.ElicitResult, error) {
		switch req.Params.Message {
		case "Provide a summary for the requested text.":
			return &sdkmcp.ElicitResult{Action: "accept", Content: map[string]any{"summary": "Protocol migration complete"}}, nil
		case "Who is the summary for?":
			return &sdkmcp.ElicitResult{Action: "accept", Content: map[string]any{"audience": "SDK maintainers"}}, nil
		default:
			t.Fatalf("unexpected elicitation message %q", req.Params.Message)
			return nil, nil
		}
	}
}

type recordedMCPRequest struct {
	method    string
	sessionID string
}

type mcpRequestRecorder struct {
	base  http.RoundTripper
	mu    sync.Mutex
	calls []recordedMCPRequest
}

func (r *mcpRequestRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	var envelope struct {
		Method string `json:"method"`
	}
	if len(body) > 0 && json.Unmarshal(body, &envelope) == nil && envelope.Method != "" {
		r.mu.Lock()
		r.calls = append(r.calls, recordedMCPRequest{
			method:    envelope.Method,
			sessionID: req.Header.Get("Mcp-Session-Id"),
		})
		r.mu.Unlock()
	}
	base := r.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func (r *mcpRequestRecorder) methodCalls(method string) []recordedMCPRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	var calls []recordedMCPRequest
	for _, call := range r.calls {
		if call.method == method {
			calls = append(calls, call)
		}
	}
	return calls
}
