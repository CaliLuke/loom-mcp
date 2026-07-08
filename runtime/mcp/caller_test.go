package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const sdkStdioHelperEnv = "GOA_MCP_SDK_STDIO_HELPER"

func init() {
	otel.SetTextMapPropagator(propagation.TraceContext{})
}

func TestDefaultMCPHTTPClientUsesPhaseTimeoutsWithoutExchangeTimeout(t *testing.T) {
	t.Parallel()

	client := mcpHTTPClient(nil)

	require.Zero(t, client.Timeout)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, defaultMCPHTTPHeaderTimeout, transport.ResponseHeaderTimeout)
	require.Equal(t, defaultMCPHTTPTLSHandshake, transport.TLSHandshakeTimeout)
	require.Equal(t, defaultMCPHTTPExpectContinue, transport.ExpectContinueTimeout)
	require.NotNil(t, transport.DialContext)
}

func TestCallToolAcrossProtocols(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		newCaller func(t *testing.T, ctx context.Context) *SessionCaller
	}{
		{
			name: "stdio",
			newCaller: func(t *testing.T, ctx context.Context) *SessionCaller {
				t.Helper()

				caller, err := NewStdioCaller(ctx, StdioOptions{
					Command:     os.Args[0],
					Args:        []string{"-test.run=TestSDKStdioServerProcess", "--"},
					Env:         append(os.Environ(), sdkStdioHelperEnv+"=1"),
					InitTimeout: 5 * time.Second,
				})
				require.NoError(t, err)
				return caller
			},
		},
		{
			name: "streamable_http",
			newCaller: func(t *testing.T, ctx context.Context) *SessionCaller {
				t.Helper()

				server := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server {
					return newSDKTestServer()
				}, &sdkmcp.StreamableHTTPOptions{
					DisableLocalhostProtection: true,
				}))
				t.Cleanup(server.Close)

				caller, err := NewHTTPCaller(ctx, HTTPOptions{
					Endpoint:    server.URL,
					InitTimeout: 5 * time.Second,
				})
				require.NoError(t, err)
				return caller
			},
		},
		{
			name: "sse",
			newCaller: func(t *testing.T, ctx context.Context) *SessionCaller {
				t.Helper()

				server := httptest.NewServer(sdkmcp.NewSSEHandler(func(*http.Request) *sdkmcp.Server {
					return newSDKTestServer()
				}, nil))
				t.Cleanup(server.Close)

				caller, err := NewSSECaller(ctx, HTTPOptions{
					Endpoint:    server.URL,
					InitTimeout: 5 * time.Second,
				})
				require.NoError(t, err)
				return caller
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, expectedTrace := contextWithTrace()

			multiCaller := tt.newCaller(t, ctx)
			assertMultiContentResponse(t, multiCaller, ctx)
			require.NoError(t, multiCaller.Close())

			traceCaller := tt.newCaller(t, ctx)
			assertTraceMetadataForwarding(t, traceCaller, ctx, expectedTrace)
			require.NoError(t, traceCaller.Close())
		})
	}
}

func TestConnectSessionDetachesLiveContextCancellationAndPreservesValues(t *testing.T) {
	t.Parallel()

	type contextKey struct{}
	parent, parentCancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "trace-value"))
	var connectedCtx context.Context
	caller, err := connectSession(parent, 0, func(sessionCtx context.Context) (*sdkmcp.ClientSession, error) {
		require.Equal(t, "trace-value", sessionCtx.Value(contextKey{}))
		require.NoError(t, sessionCtx.Err())
		connectedCtx = sessionCtx
		return nil, nil
	})
	require.NoError(t, err)
	require.NotNil(t, caller)
	parentCancel()
	require.NoError(t, connectedCtx.Err())
	require.NoError(t, caller.Close())
}

func TestConnectSessionInitializationRespectsCallerCancellationWithoutTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan struct{})
	connectErr := make(chan error, 1)

	caller, err := connectSession(ctx, 0, func(sessionCtx context.Context) (*sdkmcp.ClientSession, error) {
		close(started)
		cancel()
		select {
		case <-sessionCtx.Done():
			connectErr <- sessionCtx.Err()
			close(done)
			return nil, sessionCtx.Err()
		case <-time.After(time.Second):
			close(done)
			return nil, errors.New("connect was not canceled")
		}
	})

	require.Nil(t, caller)
	require.ErrorIs(t, err, context.Canceled)
	requireReceive(t, started)
	requireReceive(t, done)
	require.ErrorIs(t, requireReceive(t, connectErr), context.Canceled)
}

func TestConnectSessionClosesLateSessionAfterTimeout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server {
		return newSDKTestServer()
	}, &sdkmcp.StreamableHTTPOptions{
		DisableLocalhostProtection: true,
	}))
	t.Cleanup(server.Close)

	release := make(chan struct{})
	lateSession := make(chan *sdkmcp.ClientSession, 1)
	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "late-client",
		Version: "1.0.0",
	}, nil)
	transport := &sdkmcp.StreamableClientTransport{
		Endpoint: server.URL,
	}

	caller, err := connectSession(context.Background(), time.Millisecond, func(context.Context) (*sdkmcp.ClientSession, error) {
		<-release
		session, connectErr := client.Connect(context.Background(), transport, nil)
		if connectErr == nil {
			lateSession <- session
		}
		return session, connectErr
	})
	require.Error(t, err)
	require.Nil(t, caller)
	require.ErrorContains(t, err, "mcp initialize timed out")

	close(release)
	session := requireReceive(t, lateSession)
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- session.Wait()
	}()
	require.Eventually(t, func() bool {
		select {
		case <-waitDone:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func TestNormalizeSDKToolResultConcatenatesTextAcrossAllContentItems(t *testing.T) {
	t.Parallel()

	first := &sdkmcp.TextContent{Text: "{\"result\":\"hello "}
	second := &sdkmcp.TextContent{Text: "world\"}"}
	resp, err := normalizeSDKToolResult(&sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{first, second},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"result":"hello world"}`, string(resp.Result))

	var structured []map[string]any
	require.NoError(t, json.Unmarshal(resp.Structured, &structured))
	require.Len(t, structured, 2)
	assert.Equal(t, "text", structured[0]["type"])
	assert.Equal(t, "text", structured[1]["type"])
}

func TestNormalizeSDKToolResultReturnsPlainTextAsStringWhenNotJSON(t *testing.T) {
	t.Parallel()

	resp, err := normalizeSDKToolResult(&sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: "hello "},
			&sdkmcp.TextContent{Text: "world"},
			&sdkmcp.TextContent{Text: "!"},
		},
	})
	require.NoError(t, err)

	var result string
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.Equal(t, "hello world!", result)
}

func TestNormalizeSDKToolResultFallsBackToStructuredItemWhenNoTextExists(t *testing.T) {
	t.Parallel()

	resp, err := normalizeSDKToolResult(&sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.ImageContent{
				Data:     []byte("fake-image"),
				MIMEType: "image/png",
			},
		},
	})
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.Equal(t, "image", result["type"])
	assert.Equal(t, "image/png", result["mimeType"])
	assert.Equal(t, "ZmFrZS1pbWFnZQ==", result["data"])

	var structured []map[string]any
	require.NoError(t, json.Unmarshal(resp.Structured, &structured))
	require.Len(t, structured, 1)
	assert.Equal(t, "image", structured[0]["type"])
}

func TestNormalizeSDKToolResultAcceptsStructuredContentWithEmptyContent(t *testing.T) {
	t.Parallel()

	resp, err := normalizeSDKToolResult(&sdkmcp.CallToolResult{
		StructuredContent: map[string]any{"count": float64(3), "status": "ok"},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"count":3,"status":"ok"}`, string(resp.Result))
	assert.Empty(t, resp.Structured)
}

func TestNormalizeSDKToolResultRejectsEmptyStructuredContentWithEmptyContent(t *testing.T) {
	t.Parallel()

	for name, structured := range map[string]any{
		"map":   map[string]any{},
		"slice": []any{},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := normalizeSDKToolResult(&sdkmcp.CallToolResult{
				StructuredContent: structured,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "tool returned no content")
		})
	}
}

func TestNormalizeSDKToolResultStructuredOnlyErrorDoesNotPanic(t *testing.T) {
	t.Parallel()

	_, err := normalizeSDKToolResult(&sdkmcp.CallToolResult{
		IsError:           true,
		StructuredContent: map[string]any{"error": "bad"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"error":"bad"`)
}

func TestNormalizeSDKToolResultReturnsToolCallErrorForIsErrorResponses(t *testing.T) {
	t.Parallel()

	_, err := normalizeSDKToolResult(&sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: "[invalid_params] bad input\nRecovery: retry with valid arguments"},
		},
	})
	require.Error(t, err)
	var toolErr *ToolCallError
	require.ErrorAs(t, err, &toolErr)
	assert.Equal(t, "[invalid_params] bad input\nRecovery: retry with valid arguments", toolErr.Error())
}

func TestNormalizeSDKToolResultPrefersStructuredContentWhenTextIsCompact(t *testing.T) {
	t.Parallel()

	resp, err := normalizeSDKToolResult(&sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: "positive"},
		},
		StructuredContent: map[string]any{"sentiment": "positive"},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"sentiment":"positive"}`, string(resp.Result))

	var structured []map[string]any
	require.NoError(t, json.Unmarshal(resp.Structured, &structured))
	require.Len(t, structured, 1)
	assert.Equal(t, "positive", structured[0]["text"])
}

func TestSDKStdioServerProcess(t *testing.T) {
	if os.Getenv(sdkStdioHelperEnv) != "1" {
		t.Skip("helper subprocess")
	}

	runSDKStdioServerProcess()
}

func assertMultiContentResponse(t *testing.T, caller *SessionCaller, ctx context.Context) {
	t.Helper()

	resp, err := caller.CallTool(ctx, CallRequest{
		Tool:    "multi_content_tool",
		Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	var result string
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.Equal(t, "hello world!", result)

	var structured []map[string]any
	require.NoError(t, json.Unmarshal(resp.Structured, &structured))
	require.Len(t, structured, 3)

	assert.Equal(t, "text", structured[0]["type"])
	assert.Equal(t, "hello ", structured[0]["text"])
	assert.Equal(t, "text", structured[1]["type"])
	assert.Equal(t, "world", structured[1]["text"])
	assert.Equal(t, "text", structured[2]["type"])
	assert.Equal(t, "!", structured[2]["text"])
}

func assertTraceMetadataForwarding(t *testing.T, caller *SessionCaller, ctx context.Context, expectedTrace string) {
	t.Helper()

	resp, err := caller.CallTool(ctx, CallRequest{
		Tool:    "trace_meta_tool",
		Payload: json.RawMessage(`{}`),
	})
	require.NoError(t, err)

	var traceparent string
	require.NoError(t, json.Unmarshal(resp.Result, &traceparent))
	assert.Equal(t, expectedTrace, traceparent)
}

func contextWithTrace() (context.Context, string) {
	traceID := trace.TraceID{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0x00}
	spanID := trace.SpanID{0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80}
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanCtx)
	expected := fmt.Sprintf("00-%s-%s-01", traceID.String(), spanID.String())
	return ctx, expected
}

func newSDKTestServer() *sdkmcp.Server {
	server := sdkmcp.NewServer(
		&sdkmcp.Implementation{
			Name:    "test-server",
			Version: "1.0.0",
		},
		nil,
	)

	server.AddTool(&sdkmcp.Tool{
		Name:        "multi_content_tool",
		Description: "Returns multiple text items",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: "hello "},
				&sdkmcp.TextContent{Text: "world"},
				&sdkmcp.TextContent{Text: "!"},
			},
		}, nil
	})

	server.AddTool(&sdkmcp.Tool{
		Name:        "trace_meta_tool",
		Description: "Returns the incoming trace metadata",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		traceparent, _ := req.Params.Meta["traceparent"].(string)
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: traceparent},
			},
		}, nil
	})

	return server
}

func runSDKStdioServerProcess() {
	server := newSDKTestServer()
	if err := server.Run(context.Background(), &sdkmcp.StdioTransport{}); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func requireReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()

	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		var zero T
		require.Fail(t, "timed out waiting for channel receive")
		return zero
	}
}
