package assistantapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
	mcpruntime "github.com/CaliLuke/loom-mcp/runtime/mcp"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedSDKServerReturnsSpecCompliantUnauthorizedChallenge(t *testing.T) {
	t.Parallel()

	sdkServer, _ := newGeneratedSDKServer(t)

	mux := http.NewServeMux()
	verifier := func(_ context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
		if strings.TrimSpace(token) == "" {
			return nil, mcpauth.ErrInvalidToken
		}
		return &mcpauth.TokenInfo{UserID: token}, nil
	}
	protectedHandler := mcpruntime.WithOAuthChallenge(
		mcpauth.RequireBearerToken(verifier, nil)(sdkServer.Handler),
		"/rpc",
		mcpassistant.OAuthChallengeHeader,
	)
	mux.Handle("/rpc", protectedHandler)
	mountOAuthDiscovery(mux, "/rpc")

	server := httptest.NewServer(mux)
	defer server.Close()

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		server.URL+"/rpc",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"unauthenticated request to protected resource must receive 401 per MCP 2025-11-25 auth spec")

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	require.NotEmpty(t, wwwAuth, "WWW-Authenticate header MUST be present on 401 per RFC 6750")
	assert.True(t, strings.HasPrefix(wwwAuth, "Bearer "),
		"challenge scheme must be Bearer; got %q", wwwAuth)
	assert.Contains(t, wwwAuth, `resource_metadata="`,
		"challenge MUST include resource_metadata parameter pointing at the PRM URL")
	assert.Contains(t, wwwAuth, "/.well-known/oauth-protected-resource/rpc",
		"resource_metadata must point at the path-suffixed well-known URL; got %q", wwwAuth)
	assert.Contains(t, wwwAuth, `scope="read write"`,
		"challenge must include the declared scopes; got %q", wwwAuth)
}

// TestGeneratedSDKServerChallengeIgnoresMalformedForwardedHost exercises
// the CanonicalizeChallengeOrigin fallback end-to-end: when the request
// carries a malformed X-Forwarded-Host (here, one embedding a path
// separator — a common shape for would-be SSRF/open-redirect tricks),
// the emitted WWW-Authenticate header must not echo attacker bytes. It
// falls back to the request host rather than failing the request
// because a challenge is a formatting artifact inside an already-401
// response.
//
// CRLF-injection cases are covered at the unit level
// (runtime/mcp/oauth_adversarial_test.go) because Go's net/http client
// refuses to send a request whose header value contains CR/LF.
func TestGeneratedSDKServerChallengeIgnoresMalformedForwardedHost(t *testing.T) {
	t.Parallel()

	sdkServer, _ := newGeneratedSDKServer(t)

	mux := http.NewServeMux()
	verifier := func(_ context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
		if strings.TrimSpace(token) == "" {
			return nil, mcpauth.ErrInvalidToken
		}
		return &mcpauth.TokenInfo{UserID: token}, nil
	}
	protectedHandler := mcpruntime.WithOAuthChallenge(
		mcpauth.RequireBearerToken(verifier, nil)(sdkServer.Handler),
		"/rpc",
		mcpassistant.OAuthChallengeHeader,
	)
	mux.Handle("/rpc", protectedHandler)

	server := httptest.NewServer(mux)
	defer server.Close()

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		server.URL+"/rpc",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("X-Forwarded-Host", "evil.example.com/attack-path")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	require.NotEmpty(t, wwwAuth)
	assert.NotContains(t, wwwAuth, "evil.example.com",
		"challenge must not reflect attacker-controlled forwarded host when it was malformed; got %q", wwwAuth)
	assert.NotContains(t, wwwAuth, "attack-path",
		"challenge must not reflect the attacker path component; got %q", wwwAuth)
}

// TestGeneratedSDKServerMetadataPinnedResourceIgnoresMalformedForwarded
// documents the "pin ResourceIdentifier for safety" posture: because the
// assistant fixture declares ResourceIdentifier, the PRM handler returns
// that declared value regardless of any forwarded header manipulation.
// This is the recommended production configuration.
func TestGeneratedSDKServerMetadataPinnedResourceIgnoresMalformedForwarded(t *testing.T) {
	t.Parallel()

	_, sdkHTTPServer := newGeneratedSDKServer(t)
	defer sdkHTTPServer.Close()

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		sdkHTTPServer.URL+"/.well-known/oauth-protected-resource/rpc",
		nil,
	)
	require.NoError(t, err)
	req.Header.Set("X-Forwarded-Host", "evil.example.com/attack-path")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()

	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"pinned ResourceIdentifier makes the PRM handler independent of forwarded headers; a malformed header must not fail the request either")

	var doc map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&doc))
	assert.Equal(t, "https://api.example.com/mcp", doc["resource"],
		"pinned resource must be returned verbatim regardless of request headers")
}
