package assistantapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
	mcpruntime "github.com/CaliLuke/loom-mcp/runtime/mcp"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// audienceVerifier returns a TokenVerifier that decodes the opaque
// bearer token as the single claim value stored under TokenInfo.Extra["aud"].
// This is enough to drive the generated EnforceAudience wrapper through
// its three accepted claim shapes (string, []string, []any).
func audienceVerifier(claim any) mcpauth.TokenVerifier {
	return func(_ context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
		if strings.TrimSpace(token) == "" {
			return nil, mcpauth.ErrInvalidToken
		}
		return &mcpauth.TokenInfo{
			UserID:     token,
			Expiration: time.Now().Add(time.Hour),
			Extra:      map[string]any{"aud": claim},
		}, nil
	}
}

// TestEnforceAudience_AcceptsMatchingStringClaim documents the basic
// audience check: a string claim equal to the DSL-declared
// ResourceIdentifier is accepted and the request proceeds.
func TestEnforceAudience_AcceptsMatchingStringClaim(t *testing.T) {
	t.Parallel()

	sdkServer, _ := newGeneratedSDKServer(t)

	verifier := mcpassistant.EnforceAudience(audienceVerifier(mcpassistant.ExpectedResourceIdentifier()))

	mux := http.NewServeMux()
	protected := mcpruntime.WithOAuthChallenge(
		mcpauth.RequireBearerToken(verifier, nil)(sdkServer.Handler),
		"/rpc",
		mcpassistant.OAuthChallengeHeader,
	)
	mux.Handle("/rpc", protected)
	mountOAuthDiscovery(mux, "/rpc")

	server := httptest.NewServer(mux)
	defer server.Close()

	resp := doProtectedInitialize(t, server.URL+"/rpc", "any-token-value")
	defer func() { require.NoError(t, resp.Body.Close()) }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestEnforceAudience_RejectsMismatchingStringClaim replaces the
// consumer-written audienceCheck from the original adversarial test
// with a single call to the generated EnforceAudience wrapper.
// Behavior: token minted for a different resource → 401 +
// WWW-Authenticate: Bearer error="invalid_token".
func TestEnforceAudience_RejectsMismatchingStringClaim(t *testing.T) {
	t.Parallel()

	sdkServer, _ := newGeneratedSDKServer(t)

	verifier := mcpassistant.EnforceAudience(audienceVerifier("https://other.example.com/mcp"))

	mux := http.NewServeMux()
	protected := mcpruntime.WithOAuthChallenge(
		mcpauth.RequireBearerToken(verifier, nil)(sdkServer.Handler),
		"/rpc",
		mcpassistant.OAuthChallengeHeader,
	)
	mux.Handle("/rpc", protected)
	mountOAuthDiscovery(mux, "/rpc")

	server := httptest.NewServer(mux)
	defer server.Close()

	resp := doProtectedInitialize(t, server.URL+"/rpc", "issued-for-other")
	defer func() { require.NoError(t, resp.Body.Close()) }()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"audience mismatch MUST yield 401 per RFC 6750 §3.1 invalid_token")
	wwwAuth := resp.Header.Get("WWW-Authenticate")
	require.NotEmpty(t, wwwAuth,
		"401 from RequireBearerToken(EnforceAudience(...)) must carry a Bearer challenge; got empty header")
	assert.Contains(t, wwwAuth, "resource_metadata=",
		"challenge must point clients at the Protected Resource Metadata document; got %q", wwwAuth)
	// The generic WithOAuthChallenge augmenter is error-agnostic, so it
	// emits the scope-listing challenge rather than the
	// `error="invalid_token"` form. The 401 + PRM pointer still lets the
	// client recover; the error-aware variant is tracked as a follow-up
	// in MCP_AUTH_MODERNIZATION_PLAN.md.
}

// TestEnforceAudience_AcceptsMatchingArrayClaim covers the RFC 7519 §4.1.3
// `aud` array shape. A JWT decoded by encoding/json typically yields
// []any of strings, so both []string and []any must pass as long as one
// element matches.
func TestEnforceAudience_AcceptsMatchingArrayClaim(t *testing.T) {
	t.Parallel()

	cases := map[string]any{
		"[]string":             []string{"https://other.example.com", mcpassistant.ExpectedResourceIdentifier()},
		"[]any of strings":     []any{"https://other.example.com", mcpassistant.ExpectedResourceIdentifier()},
		"[]any with non-str 1": []any{42, mcpassistant.ExpectedResourceIdentifier()},
	}
	for name, claim := range cases {
		t.Run(name, func(t *testing.T) {
			sdkServer, _ := newGeneratedSDKServer(t)

			verifier := mcpassistant.EnforceAudience(audienceVerifier(claim))

			mux := http.NewServeMux()
			mux.Handle("/rpc", mcpauth.RequireBearerToken(verifier, nil)(sdkServer.Handler))
			mountOAuthDiscovery(mux, "/rpc")

			server := httptest.NewServer(mux)
			defer server.Close()

			resp := doProtectedInitialize(t, server.URL+"/rpc", "token")
			defer func() { require.NoError(t, resp.Body.Close()) }()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}
}

// TestEnforceAudience_RejectsMissingOrWrongTypeClaim makes sure missing
// or wrong-typed `aud` claims fail closed. A JWT without an audience
// claim or with a numeric audience must NOT be admitted just because
// EnforceAudience cannot compare the value — the absence of a match is
// itself a mismatch.
func TestEnforceAudience_RejectsMissingOrWrongTypeClaim(t *testing.T) {
	t.Parallel()

	cases := map[string]any{
		"missing":        nil,
		"numeric":        42,
		"map":            map[string]string{"foo": "bar"},
		"array no str":   []any{1, 2, 3},
		"empty []string": []string{},
	}
	for name, claim := range cases {
		t.Run(name, func(t *testing.T) {
			sdkServer, _ := newGeneratedSDKServer(t)

			verifier := mcpassistant.EnforceAudience(audienceVerifier(claim))

			mux := http.NewServeMux()
			mux.Handle("/rpc", mcpauth.RequireBearerToken(verifier, nil)(sdkServer.Handler))
			mountOAuthDiscovery(mux, "/rpc")

			server := httptest.NewServer(mux)
			defer server.Close()

			resp := doProtectedInitialize(t, server.URL+"/rpc", "token")
			defer func() { require.NoError(t, resp.Body.Close()) }()
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
				"missing or wrong-typed aud claim MUST yield 401, not admit")
		})
	}
}

// TestErrAudienceMismatch_UnwrapsToInvalidToken pins the wrapping
// contract that makes go-sdk/auth.RequireBearerToken write 401 on
// audience failure instead of admitting the request.
func TestErrAudienceMismatch_UnwrapsToInvalidToken(t *testing.T) {
	t.Parallel()

	assert.ErrorIs(t, mcpassistant.ErrAudienceMismatch, mcpauth.ErrInvalidToken,
		"ErrAudienceMismatch must wrap mcpauth.ErrInvalidToken so RequireBearerToken writes 401 (WithOAuthChallenge then augments with the Bearer challenge)")

	// Also exercise the unwrap directly so a future refactor can't
	// silently break the contract while keeping the ErrorIs happy.
	var target error = mcpauth.ErrInvalidToken
	assert.True(t, errors.Is(mcpassistant.ErrAudienceMismatch, target))
}

func doProtectedInitialize(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		url,
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"enforce-audience-test","version":"1.0.0"}}}`),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}
