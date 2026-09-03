package sdkbridge

import (
	"context"
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

func TestNewServerRejectsInvalidSessionBeforeSDKDispatch(t *testing.T) {
	errInvalidSession := errors.New("invalid session")
	asserted := false
	server, err := NewServer(Config{
		CompatibilityVersion: CompatibilityVersion,
		Implementation:       mcpsdk.Implementation{Name: "test", Version: "1.0.0"},
		Sessions: SessionHooks{
			AssertPrincipal: func(_ context.Context, sessionID string) error {
				asserted = true
				assert.Equal(t, "missing", sessionID)
				return errInvalidSession
			},
			IsInvalidSessionID: func(err error) bool {
				return errors.Is(err, errInvalidSession)
			},
		},
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://example.com/mcp", nil)
	req.Header.Set(mcpruntime.HeaderKeySessionID, "missing")
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, req)

	assert.True(t, asserted)
	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Contains(t, response.Body.String(), "invalid session")
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

func TestValidateOriginChecksEveryHTTPMethod(t *testing.T) {
	protection := http.NewCrossOriginProtection()
	protection.AddTrustedOrigin("https://trusted.example")

	tests := []struct {
		name      string
		method    string
		origins   []string
		fetchSite string
		wantErr   bool
	}{
		{name: "missing", method: http.MethodGet},
		{name: "missing on cross-site POST", method: http.MethodPost, fetchSite: "cross-site", wantErr: true},
		{name: "same origin GET", method: http.MethodGet, origins: []string{"https://server.example"}},
		{name: "trusted GET", method: http.MethodGet, origins: []string{"https://trusted.example"}},
		{name: "untrusted GET", method: http.MethodGet, origins: []string{"https://evil.example"}, wantErr: true},
		{name: "untrusted HEAD", method: http.MethodHead, origins: []string{"https://evil.example"}, wantErr: true},
		{name: "untrusted OPTIONS", method: http.MethodOptions, origins: []string{"https://evil.example"}, wantErr: true},
		{name: "untrusted POST", method: http.MethodPost, origins: []string{"https://evil.example"}, wantErr: true},
		{name: "empty", method: http.MethodGet, origins: []string{""}, wantErr: true},
		{name: "repeated", method: http.MethodGet, origins: []string{"https://server.example", "https://trusted.example"}, wantErr: true},
		{name: "path", method: http.MethodGet, origins: []string{"https://server.example/path"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "https://server.example/rpc", nil)
			for _, origin := range test.origins {
				req.Header.Add("Origin", origin)
			}
			if test.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", test.fetchSite)
			} else {
				req.Header.Set("Sec-Fetch-Site", "same-site")
			}

			err := validateOrigin(protection, req)

			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
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
		Sessions: SessionHooks{AssertPrincipal: func(context.Context, string) error {
			assertPrincipalCalled = true
			return errors.New("must not run")
		}},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "https://server.example/mcp", nil)
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

	req := httptest.NewRequest(http.MethodOptions, "https://server.example/mcp", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, req)

	assert.Equal(t, http.StatusForbidden, response.Code)
}
func TestPromptHandlerEncodesArgumentsAndBindsRequestContext(t *testing.T) {
	initialized := false
	handler := PromptHandler(HandlerContext{
		RequestContext: func(ctx context.Context, req *http.Request) context.Context {
			assert.Equal(t, http.MethodPost, req.Method)
			assert.Equal(t, "/mcp", req.URL.Path)
			assert.Equal(t, "request", req.Header.Get("X-Source"))
			return ctx
		},
		MarkInitialized: func(sessionID string) {
			assert.Empty(t, sessionID)
			initialized = true
		},
	}, func(_ context.Context, request PromptRequest) (*mcpsdk.GetPromptResult, error) {
		assert.Equal(t, "code_review", request.Name)
		assert.JSONEq(t, `{"code":"return true"}`, string(request.Arguments))
		assert.NotNil(t, request.Bind(struct{}{}))
		return &mcpsdk.GetPromptResult{Description: "ready"}, nil
	})
	ctx := mcpruntime.WithRequestHeaders(context.Background(), http.Header{"X-Source": {"request"}})
	req := &mcpsdk.GetPromptRequest{Params: &mcpsdk.GetPromptParams{
		Name:      "code_review",
		Arguments: map[string]string{"code": "return true"},
	}}

	result, err := handler(ctx, req)

	require.NoError(t, err)
	assert.Equal(t, "ready", result.Description)
	assert.True(t, initialized)
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
