package assistantapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func TestGeneratedSDKServerGETEnforcesSessionPrincipal(t *testing.T) {
	t.Parallel()

	sdkServer, err := mcpassistant.NewSDKServer(NewAssistant(), withTestRuntimeCORS(t, nil))
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle("/rpc", sdkServer.Handler)
	server := httptest.NewServer(mux)
	defer server.Close()

	verifier := func(_ context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
		if strings.TrimSpace(token) == "" {
			return nil, mcpauth.ErrInvalidToken
		}
		return &mcpauth.TokenInfo{
			Scopes:     []string{"mcp"},
			UserID:     token,
			Expiration: time.Now().Add(time.Hour),
		}, nil
	}
	protected := httptest.NewServer(mcpauth.RequireBearerToken(verifier, nil)(server.Config.Handler))
	defer protected.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID := initializeProtectedSDKSession(t, ctx, protected.URL+"/rpc", "user-1")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, protected.URL+"/rpc", nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer user-2")
	req.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Contains(t, string(body), "session user mismatch")
}

func TestGeneratedSDKServerEnforcesSessionPrincipalOnEverySessionRequest(t *testing.T) {
	t.Parallel()

	sdkServer, err := mcpassistant.NewSDKServer(NewAssistant(), withTestRuntimeCORS(t, nil))
	require.NoError(t, err)
	server := httptest.NewServer(mcpauth.RequireBearerToken(testMCPTokenVerifier, nil)(sdkServer.Handler))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sessionID := initializeProtectedSDKSession(t, ctx, server.URL, "user-1")

	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		resp := authenticatedSessionRequest(t, ctx, server.URL, method, sessionID, "user-2")
		body, readErr := io.ReadAll(resp.Body)
		require.NoError(t, readErr)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, "%s body: %s", method, string(body))
		assert.Contains(t, string(body), "session user mismatch")
	}

	resp := authenticatedSessionRequest(t, ctx, server.URL, http.MethodPost, sessionID, "user-1")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "a rejected DELETE must not terminate the rightful principal's session")
}

func TestGeneratedSDKServerRejectsUnknownSessionIDWithNotFound(t *testing.T) {
	t.Parallel()

	sdkServer, err := mcpassistant.NewSDKServer(NewAssistant(), withTestRuntimeCORS(t, nil))
	require.NoError(t, err)
	server := httptest.NewServer(mcpauth.RequireBearerToken(testMCPTokenVerifier, nil)(sdkServer.Handler))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp := authenticatedSessionRequest(t, ctx, server.URL, http.MethodPost, "unknown-session", "user-1")
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "body: %s", string(body))
	assert.Contains(t, string(body), "invalid session ID")
}

func TestGeneratedJSONRPCServerEnforcesSessionPrincipalOnEverySessionRequest(t *testing.T) {
	t.Parallel()

	rawServer := newGeneratedJSONRPCServer(t)
	defer rawServer.Close()
	server := httptest.NewServer(mcpauth.RequireBearerToken(testMCPTokenVerifier, nil)(rawServer.Config.Handler))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sessionID := initializeProtectedSDKSession(t, ctx, server.URL+"/rpc", "user-1")

	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		resp := authenticatedSessionRequest(t, ctx, server.URL+"/rpc", method, sessionID, "user-2")
		body, readErr := io.ReadAll(resp.Body)
		require.NoError(t, readErr)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusForbidden, resp.StatusCode, "%s body: %s", method, string(body))
		assert.Contains(t, string(body), "session user mismatch")
	}

	resp := authenticatedSessionRequest(t, ctx, server.URL+"/rpc", http.MethodPost, sessionID, "user-1")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "a rejected DELETE must not terminate the rightful principal's session")
}

func TestGeneratedJSONRPCServerRejectsAuthenticatedSessionWithoutPrincipalBinding(t *testing.T) {
	t.Parallel()

	rawServer := newGeneratedJSONRPCServer(t)
	defer rawServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The initialization bypasses authentication, so the session is deliberately
	// issued without a principal binding. An authenticated request must not be
	// allowed to adopt that session later.
	sessionID := initializeProtectedSDKSession(t, ctx, rawServer.URL+"/rpc", "unverified-user")
	protected := httptest.NewServer(mcpauth.RequireBearerToken(testMCPTokenVerifier, nil)(rawServer.Config.Handler))
	defer protected.Close()

	resp := authenticatedSessionRequest(t, ctx, protected.URL+"/rpc", http.MethodPost, sessionID, "user-1")
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "body: %s", string(body))
	assert.Contains(t, string(body), "session principal binding missing")
}

func TestGeneratedJSONRPCServerValidatesSessionHeaderOnInitialize(t *testing.T) {
	t.Parallel()

	rawServer := newGeneratedJSONRPCServer(t)
	defer rawServer.Close()
	server := httptest.NewServer(mcpauth.RequireBearerToken(testMCPTokenVerifier, nil)(rawServer.Config.Handler))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ownerSessionID := initializeProtectedSDKSession(t, ctx, server.URL+"/rpc", "user-1")

	for _, test := range []struct {
		name         string
		sessionID    string
		token        string
		wantStatus   int
		wantResponse string
	}{
		{name: "existing owner session", sessionID: ownerSessionID, token: "user-1", wantStatus: http.StatusOK, wantResponse: "Already initialized"},
		{name: "existing foreign session", sessionID: ownerSessionID, token: "user-2", wantStatus: http.StatusForbidden, wantResponse: "session user mismatch"},
		{name: "attacker chosen session", sessionID: "attacker-session", token: "user-2", wantStatus: http.StatusNotFound, wantResponse: "Invalid or expired session ID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resp := authenticatedInitializeRequest(t, ctx, server.URL+"/rpc", test.sessionID, test.token)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, test.wantStatus, resp.StatusCode, "body: %s", string(body))
			assert.Contains(t, string(body), test.wantResponse)
		})
	}

	resp := authenticatedSessionRequest(t, ctx, server.URL+"/rpc", http.MethodPost, ownerSessionID, "user-1")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "initialize validation must not invalidate the owner's session")
}

func testMCPTokenVerifier(_ context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
	if strings.TrimSpace(token) == "" {
		return nil, mcpauth.ErrInvalidToken
	}
	return &mcpauth.TokenInfo{
		Scopes:     []string{"mcp"},
		UserID:     token,
		Expiration: time.Now().Add(time.Hour),
	}, nil
}

func authenticatedSessionRequest(t *testing.T, ctx context.Context, endpoint, method, sessionID, token string) *http.Response {
	t.Helper()

	var body io.Reader
	if method == http.MethodPost {
		payload, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      "resources-list-1",
			"method":  "resources/list",
			"params":  map[string]any{},
		})
		require.NoError(t, err)
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
	req.Header.Set(mcpruntime.HeaderKeyProtocolVersion, "2025-11-25")
	if method == http.MethodGet {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json, text/event-stream")
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func authenticatedInitializeRequest(t *testing.T, ctx context.Context, endpoint, sessionID, token string) *http.Response {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "init-with-session",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-11-25",
			"clientInfo": map[string]any{
				"name":    "sdk-auth-itest",
				"version": "1.0.0",
			},
		},
	})
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(mcpruntime.HeaderKeyProtocolVersion, "2025-11-25")
	req.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func initializeProtectedSDKSession(t *testing.T, ctx context.Context, endpoint string, token string) string {
	t.Helper()

	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      "init-1",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-11-25",
			"clientInfo": map[string]any{
				"name":    "sdk-auth-itest",
				"version": "1.0.0",
			},
		},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "initialize body: %s", string(data))

	sessionID := resp.Header.Get(mcpruntime.HeaderKeySessionID)
	require.NotEmpty(t, sessionID)
	return sessionID
}
