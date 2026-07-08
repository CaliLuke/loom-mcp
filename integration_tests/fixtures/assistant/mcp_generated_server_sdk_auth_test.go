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

	sdkServer, err := mcpassistant.NewSDKServer(NewAssistant(), nil)
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
