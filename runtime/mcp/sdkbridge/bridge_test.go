package sdkbridge

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	loomhttp "github.com/CaliLuke/loom/http"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
