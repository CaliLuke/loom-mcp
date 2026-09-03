package assistantapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestGeneratedSDKServerRejectsInvalidOrigin(t *testing.T) {
	t.Parallel()

	_, sdkHTTPServer := newGeneratedSDKServer(t)
	defer sdkHTTPServer.Close()

	resp := postSDKInitializeWithOrigin(t, sdkHTTPServer.URL+"/rpc", "https://evil.example.com")
	require.Equal(t, http.StatusForbidden, resp.StatusCode,
		"SDK streamable HTTP server must reject untrusted Origin with 403 per MCP 2025-11-25 spec")
}

func TestGeneratedSDKServerRejectsInvalidOriginOnSSE(t *testing.T) {
	t.Parallel()

	_, sdkHTTPServer := newGeneratedSDKServer(t)
	defer sdkHTTPServer.Close()

	endpoint := sdkHTTPServer.URL + "/rpc"
	session := connectSDKSessionToServer(t, endpoint, nil)
	defer func() {
		require.NoError(t, session.Close())
	}()

	tests := []struct {
		name    string
		origins []string
	}{
		{name: "untrusted", origins: []string{"https://evil.example.com"}},
		{name: "empty", origins: []string{""}},
		{name: "multiple", origins: []string{sdkHTTPServer.URL, "https://evil.example.com"}},
		{name: "malformed", origins: []string{sdkHTTPServer.URL + "/"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			require.NoError(t, err)
			req.Header.Set("Accept", "text/event-stream")
			req.Header["Origin"] = tc.origins
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set(mcpruntime.HeaderKeyProtocolVersion, "2025-11-25")
			req.Header.Set(mcpruntime.HeaderKeySessionID, session.ID())

			resp, err := sdkHTTPServer.Client().Do(req)
			require.NoError(t, err)
			require.Equal(t, http.StatusForbidden, resp.StatusCode,
				"SDK streamable HTTP server must reject an invalid Origin on the GET SSE connection per MCP 2025-11-25 spec")
			require.NoError(t, resp.Body.Close())
		})
	}
}

func TestGeneratedSDKServerKeepsUnsafeMethodBypassScoped(t *testing.T) {
	t.Parallel()

	protection := http.NewCrossOriginProtection()
	protection.AddInsecureBypassPattern("POST /rpc")
	sdkServer, err := mcpassistant.NewSDKServer(NewAssistant(), &mcpassistant.SDKServerOptions{
		PromptProvider: promptProvider{},
		StreamableHTTP: &mcp.StreamableHTTPOptions{CrossOriginProtection: protection},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "http://mcp.example/rpc", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	recorder := httptest.NewRecorder()
	sdkServer.Handler.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusForbidden, recorder.Code,
		"an unsafe POST bypass must not exempt the GET SSE connection")
}

func TestGeneratedSDKServerAllowsSameOrigin(t *testing.T) {
	t.Parallel()

	_, sdkHTTPServer := newGeneratedSDKServer(t)
	defer sdkHTTPServer.Close()

	resp := postSDKInitializeWithOrigin(t, sdkHTTPServer.URL+"/rpc", sdkHTTPServer.URL)
	require.NotEqual(t, http.StatusForbidden, resp.StatusCode,
		"SDK streamable HTTP server must not reject same-origin requests")
}
func postSDKInitializeWithOrigin(t *testing.T, endpoint, origin string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		endpoint,
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"origin-test","version":"1.0.0"}}}`)),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Origin", origin)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, resp.Body.Close())
	})
	return resp
}
