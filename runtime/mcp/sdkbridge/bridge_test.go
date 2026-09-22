package sdkbridge

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	loomhttp "github.com/CaliLuke/loom/http"
	"github.com/CaliLuke/loom/observability/transport"
	sdkjsonrpc "github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type bodyReadCounter struct {
	reader io.Reader
	reads  int
	bytes  int
}

type failingStreamWriter struct {
	header http.Header
}

func (c *bodyReadCounter) Read(buffer []byte) (int, error) {
	c.reads++
	n, err := c.reader.Read(buffer)
	c.bytes += n
	return n, err
}

func (w *failingStreamWriter) Header() http.Header {
	return w.header
}

func (w *failingStreamWriter) WriteHeader(int) {}

func (w *failingStreamWriter) Write([]byte) (int, error) {
	return 0, errors.New("stream write failed")
}

func TestNewServerRejectsGeneratedRuntimeVersionMismatch(t *testing.T) {
	server, err := NewServer(Config{
		CompatibilityVersion: CompatibilityVersion - 1,
		Implementation:       mcpsdk.Implementation{Name: "test", Version: "1.0.0"},
	})

	require.EqualError(t, err, fmt.Sprintf(
		"MCP SDK bridge compatibility mismatch: generated version %d, runtime version %d",
		CompatibilityVersion-1,
		CompatibilityVersion,
	))
	assert.Nil(t, server)
}

func TestNewServerAcceptsSameCompatibilityVersion(t *testing.T) {
	server, err := NewServer(Config{
		CompatibilityVersion: CompatibilityVersion,
		Implementation:       mcpsdk.Implementation{Name: "same-version", Version: "1.0.0"},
	})

	require.NoError(t, err)
	assert.NotNil(t, server)
}

func TestObserveSDKRequestSummaryEnrichesTerminalEvent(t *testing.T) {
	t.Parallel()

	numericID, err := sdkjsonrpc.MakeID(float64(42))
	require.NoError(t, err)
	stringID, err := sdkjsonrpc.MakeID("request-7")
	require.NoError(t, err)
	tests := []struct {
		name             string
		summary          mcpsdk.StreamableHTTPRequestSummary
		wantMethod       string
		wantID           string
		wantNotification bool
	}{
		{name: "numeric call", summary: mcpsdk.StreamableHTTPRequestSummary{Method: "tools/list", RequestID: numericID}, wantMethod: "tools/list", wantID: "42"},
		{name: "string call", summary: mcpsdk.StreamableHTTPRequestSummary{Method: "tools/call", RequestID: stringID}, wantMethod: "tools/call", wantID: "request-7"},
		{name: "notification", summary: mcpsdk.StreamableHTTPRequestSummary{Method: "notifications/initialized", IsNotification: true}, wantMethod: "notifications/initialized", wantNotification: true},
		{name: "response", summary: mcpsdk.StreamableHTTPRequestSummary{IsResponse: true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var events []transport.Event
			ctx := transport.WithObserver(t.Context(), transport.ObserverFunc(func(_ context.Context, event transport.Event) {
				events = append(events, event)
			}))
			observation := transport.BeginRequest(ctx, transport.TransportHTTP, "mcp", http.MethodPost)
			observation.SetSession("session-1")
			ctx = transport.WithRequestObserver(ctx, observation)

			streamableHTTPOptions(nil).OnRequestSummary(ctx, test.summary)
			observation.End()

			require.Len(t, events, 2)
			terminal := events[1]
			assert.Equal(t, test.wantMethod, terminal.JSONRPCMethod)
			assert.Equal(t, test.wantID, terminal.JSONRPCID)
			assert.Equal(t, test.wantNotification, terminal.Notification)
			assert.Equal(t, "session-1", terminal.SessionID)
		})
	}
}

func TestObservedStreamEventsKeepParsedProtocolFields(t *testing.T) {
	requestID, err := sdkjsonrpc.MakeID("stream-1")
	require.NoError(t, err)
	var events []transport.Event
	ctx := transport.WithObserver(t.Context(), transport.ObserverFunc(func(_ context.Context, event transport.Event) {
		events = append(events, event)
	}))
	handler := observeHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observeSDKRequestSummary(r.Context(), mcpsdk.StreamableHTTPRequestSummary{Method: "tools/list", RequestID: requestID})
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set(mcpruntime.HeaderKeySessionID, "issued-session")
		w.WriteHeader(http.StatusOK)
		_, writeErr := w.Write([]byte("data: ready\n\n"))
		assert.NoError(t, writeErr)
	}))
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "http://example.com/mcp", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	require.Len(t, events, 4)
	assert.Equal(t, transport.EventKindRequestStart, events[0].Kind)
	assert.Empty(t, events[0].JSONRPCMethod)
	assert.Equal(t, transport.EventKindStreamOpen, events[1].Kind)
	assert.Equal(t, http.StatusOK, events[1].StatusCode)
	assert.Equal(t, transport.EventKindStreamClose, events[2].Kind)
	assert.Positive(t, events[2].BytesWritten)
	assert.Equal(t, transport.EventKindRequestFinish, events[3].Kind)
	for _, event := range events[1:] {
		assert.Equal(t, "tools/list", event.JSONRPCMethod)
		assert.Equal(t, "stream-1", event.JSONRPCID)
		assert.Equal(t, "issued-session", event.SessionID)
	}
}

func TestObservedStreamWriteFailureHasNoNaturalClose(t *testing.T) {
	var events []transport.Event
	ctx := transport.WithObserver(t.Context(), transport.ObserverFunc(func(_ context.Context, event transport.Event) {
		events = append(events, event)
	}))
	handler := observeHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observeSDKRequestSummary(r.Context(), mcpsdk.StreamableHTTPRequestSummary{Method: "tools/list"})
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, writeErr := w.Write([]byte("data: ready\n\n"))
		assert.Error(t, writeErr)
	}))
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "http://example.com/mcp", nil)
	handler.ServeHTTP(&failingStreamWriter{header: make(http.Header)}, request)

	require.Len(t, events, 4)
	assert.Equal(t, transport.EventKindRequestStart, events[0].Kind)
	assert.Equal(t, transport.EventKindStreamOpen, events[1].Kind)
	assert.Equal(t, transport.EventKindStreamFailure, events[2].Kind)
	assert.Equal(t, transport.EventKindRequestFailure, events[3].Kind)
	assert.Equal(t, transport.ReasonStreamWriteFailed, events[3].Reason)
	for _, event := range events[1:] {
		assert.Equal(t, "tools/list", event.JSONRPCMethod)
	}
}

func TestObservedImplicitWritePreservesContentType(t *testing.T) {
	handler := observeHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte("plain text"))
		assert.NoError(t, err)
	}))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/mcp", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	result := response.Result()
	t.Cleanup(func() {
		require.NoError(t, result.Body.Close())
	})
	assert.Equal(t, "text/plain; charset=utf-8", result.Header.Get("Content-Type"))
}

func TestObservedImplicitStreamWriteReportsStatus(t *testing.T) {
	var events []transport.Event
	ctx := transport.WithObserver(t.Context(), transport.ObserverFunc(func(_ context.Context, event transport.Event) {
		events = append(events, event)
	}))
	handler := observeHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observeSDKRequestSummary(r.Context(), mcpsdk.StreamableHTTPRequestSummary{Method: "tools/list"})
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.Write([]byte("data: ready\n\n"))
		assert.NoError(t, err)
	}))
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "http://example.com/mcp", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	require.Len(t, events, 4)
	assert.Equal(t, transport.EventKindRequestStart, events[0].Kind)
	assert.Equal(t, transport.EventKindStreamOpen, events[1].Kind)
	assert.Equal(t, http.StatusOK, events[1].StatusCode)
	assert.Equal(t, "tools/list", events[1].JSONRPCMethod)
	assert.Equal(t, transport.EventKindStreamClose, events[2].Kind)
	assert.Equal(t, transport.EventKindRequestFinish, events[3].Kind)
}

func TestObservedOversizedRequestHasNoProtocolFields(t *testing.T) {
	var events []transport.Event
	server, err := NewServer(Config{
		CompatibilityVersion: CompatibilityVersion,
		Implementation:       mcpsdk.Implementation{Name: "test", Version: "1.0.0"},
		Options: Options{
			TransportObserver: transport.ObserverFunc(func(_ context.Context, event transport.Event) {
				events = append(events, event)
			}),
			StreamableHTTP: &StreamableHTTPOptions{Stateless: true, MaxRequestBodyBytes: 8},
		},
	})
	require.NoError(t, err)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://example.com/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	require.Len(t, events, 2)
	terminal := events[1]
	assert.Equal(t, transport.EventKindRequestFailure, terminal.Kind)
	assert.Empty(t, terminal.JSONRPCMethod)
	assert.Empty(t, terminal.JSONRPCID)
	assert.Zero(t, terminal.BatchCount)
	assert.Equal(t, response.Code, terminal.StatusCode)
	assert.Positive(t, terminal.BytesWritten)
}

func TestTransportObserverDoesNotChangeRequestBodyHandling(t *testing.T) {
	const body = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"observer-test","version":"1.0"}}}`
	type result struct {
		reads        int
		bytes        int
		bodyReplaced bool
		status       int
	}
	run := func(observe bool) result {
		options := Options{StreamableHTTP: &StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: -1}}
		if observe {
			options.TransportObserver = transport.ObserverFunc(func(context.Context, transport.Event) {})
		}
		server, err := NewServer(Config{
			CompatibilityVersion: CompatibilityVersion,
			Implementation:       mcpsdk.Implementation{Name: "test", Version: "1.0.0"},
			Options:              options,
		})
		require.NoError(t, err)
		counter := &bodyReadCounter{reader: strings.NewReader(body)}
		originalBody := io.NopCloser(counter)
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://example.com/mcp", nil)
		request.Body = originalBody
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		response := httptest.NewRecorder()
		server.Handler.ServeHTTP(response, request)
		return result{reads: counter.reads, bytes: counter.bytes, bodyReplaced: request.Body != originalBody, status: response.Code}
	}

	withoutObserver := run(false)
	withObserver := run(true)
	assert.Equal(t, withoutObserver, withObserver)
	assert.Equal(t, http.StatusOK, withObserver.status)
}

func TestServerOptionsDoNotAdvertiseDeprecatedDefaultCapabilities(t *testing.T) {
	configured := serverOptions(nil, nil, nil)

	encoded, err := json.Marshal(configured.Capabilities)
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(encoded))
}
func TestNewServerRejectsInvalidSessionBeforeSDKDispatch(t *testing.T) {
	server, err := NewServer(Config{
		CompatibilityVersion: CompatibilityVersion,
		Implementation:       mcpsdk.Implementation{Name: "test", Version: "1.0.0"},
		Sessions:             NewSessionState(nil),
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://example.com/mcp", nil)
	req.Header.Set(mcpruntime.HeaderKeySessionID, "missing")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, req)

	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Contains(t, response.Body.String(), "invalid session ID")
}

func TestNewServerReportsGeneratedDescriptorFailure(t *testing.T) {
	descriptorErr := errors.New("catalog unavailable")
	server, err := NewServer(Config{
		CompatibilityVersion: CompatibilityVersion,
		Implementation:       mcpsdk.Implementation{Name: "test", Version: "1.0.0"},
		Tools: func() ([]ToolBinding, error) {
			return nil, descriptorErr
		},
	})

	require.ErrorContains(t, err, "load MCP SDK tool bindings: catalog unavailable")
	assert.Nil(t, server)
}

func TestToolHandlerAcceptsNilSDKRequest(t *testing.T) {
	called := false
	handler := ToolHandler(HandlerContext{}, func(ctx context.Context, request ToolRequest) (*mcpsdk.CallToolResult, error) {
		called = true
		assert.Empty(t, request.Name)
		assert.Empty(t, request.Arguments)
		assert.NotNil(t, request.Bind(nil))
		return &mcpsdk.CallToolResult{}, nil
	})

	result, err := handler(context.Background(), nil)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, called)
}

func TestToolHandlerPreservesProgressTokenAfterPayloadBinding(t *testing.T) {
	handler := ToolHandler(HandlerContext{}, func(ctx context.Context, request ToolRequest) (*mcpsdk.CallToolResult, error) {
		bound := request.Bind(struct{}{})
		token, ok := mcpruntime.ProgressTokenFromContext(bound)
		require.True(t, ok)
		assert.Equal(t, "progress-1", token)
		return &mcpsdk.CallToolResult{}, nil
	})
	req := &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{Meta: mcpsdk.Meta{"progressToken": "progress-1"}}}

	_, err := handler(context.Background(), req)

	require.NoError(t, err)
}

func TestRequestContextMiddlewarePropagatesReturnedContextOnce(t *testing.T) {
	type contextKey struct{}
	callbackCount := 0
	middleware := requestContextMiddleware(func(ctx context.Context, req *http.Request) context.Context {
		callbackCount++
		assert.Equal(t, "call-1", req.Header.Get("X-Request-ID"))
		require.NotNil(t, req.URL)
		assert.Empty(t, req.URL.Path)
		return context.WithValue(ctx, contextKey{}, "tenant-1")
	}, NewSessionState(nil))
	handler := middleware(func(ctx context.Context, _ string, _ mcpsdk.Request) (mcpsdk.Result, error) {
		assert.Equal(t, "tenant-1", ctx.Value(contextKey{}))
		return &mcpsdk.CallToolResult{}, nil
	})
	header := make(http.Header)
	header.Set("X-Request-ID", "call-1")
	req := &mcpsdk.CallToolRequest{Extra: &mcpsdk.RequestExtra{Header: header}}

	_, err := handler(t.Context(), "tools/call", req)

	require.NoError(t, err)
	assert.Equal(t, 1, callbackCount)
}

func TestOriginValidationChecksEveryHTTPMethod(t *testing.T) {
	protected, err := originValidationHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), &OriginProtection{TrustedOrigins: []string{"https://trusted.example"}})
	require.NoError(t, err)

	tests := []struct {
		name      string
		method    string
		origins   []string
		fetchSite string
		wantCode  int
	}{
		{name: "missing", method: http.MethodGet, wantCode: http.StatusNoContent},
		{name: "missing on cross-site GET", method: http.MethodGet, fetchSite: "cross-site", wantCode: http.StatusForbidden},
		{name: "missing on cross-site POST", method: http.MethodPost, fetchSite: "cross-site", wantCode: http.StatusForbidden},
		{name: "same origin GET", method: http.MethodGet, origins: []string{"https://server.example"}, wantCode: http.StatusNoContent},
		{name: "same-host cross-site GET", method: http.MethodGet, origins: []string{"http://server.example"}, fetchSite: "cross-site", wantCode: http.StatusForbidden},
		{name: "trusted cross-site GET", method: http.MethodGet, origins: []string{"https://trusted.example"}, fetchSite: "cross-site", wantCode: http.StatusNoContent},
		{name: "untrusted GET", method: http.MethodGet, origins: []string{"https://evil.example"}, wantCode: http.StatusForbidden},
		{name: "untrusted same-origin metadata", method: http.MethodGet, origins: []string{"https://evil.example"}, fetchSite: "same-origin", wantCode: http.StatusForbidden},
		{name: "untrusted none metadata", method: http.MethodPost, origins: []string{"https://evil.example"}, fetchSite: "none", wantCode: http.StatusForbidden},
		{name: "untrusted HEAD", method: http.MethodHead, origins: []string{"https://evil.example"}, wantCode: http.StatusForbidden},
		{name: "untrusted OPTIONS", method: http.MethodOptions, origins: []string{"https://evil.example"}, wantCode: http.StatusForbidden},
		{name: "untrusted POST", method: http.MethodPost, origins: []string{"https://evil.example"}, wantCode: http.StatusForbidden},
		{name: "empty", method: http.MethodGet, origins: []string{""}, wantCode: http.StatusForbidden},
		{name: "repeated", method: http.MethodGet, origins: []string{"https://server.example", "https://trusted.example"}, wantCode: http.StatusForbidden},
		{name: "path", method: http.MethodGet, origins: []string{"https://server.example/path"}, wantCode: http.StatusForbidden},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), test.method, "https://server.example/rpc", nil)
			for _, origin := range test.origins {
				req.Header.Add("Origin", origin)
			}
			if test.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", test.fetchSite)
			}
			response := httptest.NewRecorder()
			protected.ServeHTTP(response, req)
			assert.Equal(t, test.wantCode, response.Code)
		})
	}
}

func TestOriginValidationMarksCustomDenials(t *testing.T) {
	protected, err := originValidationHandler(http.NotFoundHandler(), &OriginProtection{
		DenyHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Origin-Denied", "true")
			w.WriteHeader(http.StatusNoContent)
		}),
	})
	require.NoError(t, err)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://server.example/rpc", nil)
	req.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()
	response := &responseObserver{ResponseWriter: recorder}

	protected.ServeHTTP(response, req)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, "true", recorder.Header().Get("X-Origin-Denied"))
	assert.True(t, response.originRejected)
}

func TestSDKTransportRequestRemovesAlreadyValidatedOriginHeaders(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://server.example/rpc", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")

	transportRequest := sdkTransportRequest(req)
	assert.Empty(t, transportRequest.Header.Get("Origin"))
	assert.Empty(t, transportRequest.Header.Get("Sec-Fetch-Site"))
	assert.Equal(t, "https://evil.example", req.Header.Get("Origin"))
	assert.Equal(t, "cross-site", req.Header.Get("Sec-Fetch-Site"))
}

func TestNewServerRejectsOriginBeforeApplicationHooks(t *testing.T) {
	requestContextCalled := false
	assertPrincipalCalled := false
	server, err := NewServer(Config{
		CompatibilityVersion: CompatibilityVersion,
		Implementation:       mcpsdk.Implementation{Name: "test", Version: "1.0.0"},
		Options: Options{RequestContext: func(ctx context.Context, _ *http.Request) context.Context {
			requestContextCalled = true
			return ctx
		}},
		Sessions: NewSessionState(func(context.Context) string {
			assertPrincipalCalled = true
			return "principal"
		}),
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://server.example/mcp", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set(mcpruntime.HeaderKeySessionID, "session")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, req)

	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.False(t, requestContextCalled)
	assert.False(t, assertPrincipalCalled)
}

func TestNewServerRejectsOriginBeforeRuntimeCORSPreflight(t *testing.T) {
	policy, err := loomhttp.NewRuntimeCORSPolicy(loomhttp.CORSPolicy{Origins: []loomhttp.CORSOrigin{{
		Pattern: "https://app.example.com",
		Methods: []string{http.MethodGet, http.MethodPost, http.MethodDelete},
	}}})
	require.NoError(t, err)
	server, err := NewServer(Config{
		CompatibilityVersion: CompatibilityVersion,
		Implementation:       mcpsdk.Implementation{Name: "test", Version: "1.0.0"},
		Options:              Options{RuntimeCORS: &policy},
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "https://server.example/mcp", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, req)

	assert.Equal(t, http.StatusForbidden, response.Code)
}
func TestPromptHandlerEncodesArgumentsAndMarksInitialization(t *testing.T) {
	sessions := NewSessionState(nil)
	handler := PromptHandler(HandlerContext{Sessions: sessions}, func(_ context.Context, request PromptRequest) (*mcpsdk.GetPromptResult, error) {
		assert.Equal(t, "code_review", request.Name)
		assert.JSONEq(t, `{"code":"return true"}`, string(request.Arguments))
		assert.NotNil(t, request.Bind(struct{}{}))
		return &mcpsdk.GetPromptResult{Description: "ready"}, nil
	})
	req := &mcpsdk.GetPromptRequest{Params: &mcpsdk.GetPromptParams{
		Name:      "code_review",
		Arguments: map[string]string{"code": "return true"},
	}}

	result, err := handler(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, "ready", result.Description)
	assert.True(t, sessions.IsInitialized(context.Background()))
}

func TestResourceHandlerBindsTypedRequest(t *testing.T) {
	handler := ResourceHandler(HandlerContext{}, func(_ context.Context, request ResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		assert.Equal(t, "doc://guide", request.URI)
		assert.NotNil(t, request.Bind(struct{}{}))
		return &mcpsdk.ReadResourceResult{}, nil
	})

	result, err := handler(context.Background(), &mcpsdk.ReadResourceRequest{Params: &mcpsdk.ReadResourceParams{URI: "doc://guide"}})

	require.NoError(t, err)
	assert.NotNil(t, result)
}
func TestPromptAndResourceHandlersAcceptNilSDKRequests(t *testing.T) {
	prompt, err := PromptHandler(HandlerContext{}, func(_ context.Context, request PromptRequest) (*mcpsdk.GetPromptResult, error) {
		assert.Empty(t, request.Name)
		assert.Empty(t, request.Arguments)
		return &mcpsdk.GetPromptResult{}, nil
	})(context.Background(), nil)
	require.NoError(t, err)
	assert.NotNil(t, prompt)

	resource, err := ResourceHandler(HandlerContext{}, func(_ context.Context, request ResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		assert.Empty(t, request.URI)
		return &mcpsdk.ReadResourceResult{}, nil
	})(context.Background(), nil)
	require.NoError(t, err)
	assert.NotNil(t, resource)
}

func TestBindCompletionContextHandlesNilAndSDKRequests(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, ctx, BindCompletionContext(ctx, nil, HandlerContext{}))
}
