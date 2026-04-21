package assistantapi

import (
	"context"
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
