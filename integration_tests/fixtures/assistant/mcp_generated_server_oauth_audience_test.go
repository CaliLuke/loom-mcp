package assistantapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedSDKServerEmitsInvalidTokenChallengeForAudienceMismatch(t *testing.T) {
	t.Parallel()

	sdkServer, _ := newGeneratedSDKServer(t)

	expectedAudience := mcpassistant.ExpectedResourceIdentifier()
	require.Equal(t, "https://api.example.com/mcp", expectedAudience,
		"generated ExpectedResourceIdentifier must echo the DSL ResourceIdentifier")

	// Verifier pretends to decode a token whose audience field is stored in
	// TokenInfo.Extra["aud"]. In a real deployment this is where JWT audience
	// validation would live inside the consumer's verifier.
	verifier := func(_ context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
		if strings.TrimSpace(token) == "" {
			return nil, mcpauth.ErrInvalidToken
		}
		return &mcpauth.TokenInfo{
			UserID:     token,
			Expiration: time.Now().Add(time.Hour),
			Extra:      map[string]any{"aud": "https://other.example.com/mcp"},
		}, nil
	}

	// audienceCheck wraps the verified handler so a decoded token whose
	// audience does not match the declared resource identifier is rejected
	// via the invalid_token challenge instead of being dispatched.
	audienceCheck := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if info := mcpauth.TokenInfoFromContext(r.Context()); info != nil {
				aud, _ := info.Extra["aud"].(string)
				if aud != expectedAudience {
					w.Header().Set("WWW-Authenticate",
						mcpassistant.OAuthInvalidTokenChallengeHeader(r, "/rpc", "token audience does not match the protected resource"))
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}

	mux := http.NewServeMux()
	mux.Handle("/rpc",
		mcpauth.RequireBearerToken(verifier, nil)(audienceCheck(sdkServer.Handler)),
	)
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
	req.Header.Set("Authorization", "Bearer issued-for-other")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"audience mismatch MUST yield 401 per RFC 6750 §3.1 invalid_token")

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	require.NotEmpty(t, wwwAuth)
	assert.Contains(t, wwwAuth, `error="invalid_token"`,
		"invalid_token challenge MUST carry error=invalid_token; got %q", wwwAuth)
	assert.Contains(t, wwwAuth, "/.well-known/oauth-protected-resource/rpc",
		"challenge must still carry resource_metadata URL; got %q", wwwAuth)
	assert.Contains(t, wwwAuth, `error_description="token audience does not match the protected resource"`,
		"challenge should surface the audience-mismatch reason; got %q", wwwAuth)
}
