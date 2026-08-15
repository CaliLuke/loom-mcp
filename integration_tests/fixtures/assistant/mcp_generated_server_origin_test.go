package assistantapi

import (
	"bytes"
	"context"
	"net/http"
	"testing"

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
