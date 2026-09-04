package framework

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rawJSONRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      any              `json:"id"`
	Result  map[string]any   `json:"result"`
	Error   *rawJSONRPCError `json:"error"`
}

type rawJSONRPCError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
}

type rawHTTPResponse struct {
	StatusCode int
	Header     http.Header
}

const legacyProtocolVersion = "2025-11-25"

func TestGeneratedServerRawInitializeNegotiatesLegacyProtocol(t *testing.T) {
	t.Parallel()

	runner := startRawMCPServer(t)
	resp, body := rawMCPPost(t, runner, rawInitializeRequest(t, "init-1", legacyProtocolVersion), nil)

	require.Equal(t, http.StatusOK, resp.StatusCode, "response body: %s", body)
	require.NotEmpty(t, resp.Header.Get("Mcp-Session-Id"))
	wire := decodeRawJSONRPCResponse(t, body)
	assert.Equal(t, "2.0", wire.JSONRPC)
	assert.Equal(t, "init-1", wire.ID)
	require.Nil(t, wire.Error)
	assert.Equal(t, legacyProtocolVersion, wire.Result["protocolVersion"])
	serverInfo, ok := wire.Result["serverInfo"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "assistant-mcp", serverInfo["name"])
}

func TestGeneratedServerRawUnknownProtocolVersionFallsBackToLatestLegacy(t *testing.T) {
	t.Parallel()

	runner := startRawMCPServer(t)
	resp, body := rawMCPPost(t, runner, rawInitializeRequest(t, "init-unsupported", "2099-01-01"), nil)

	require.Equal(t, http.StatusOK, resp.StatusCode, "response body: %s", body)
	wire := decodeRawJSONRPCResponse(t, body)
	require.Nil(t, wire.Error)
	assert.Equal(t, legacyProtocolVersion, wire.Result["protocolVersion"])
}

func TestGeneratedServerRawRejectsInvalidRequests(t *testing.T) {
	t.Parallel()

	runner := startRawMCPServer(t)
	sessionID := rawInitializedSession(t, runner)
	headers := map[string]string{
		"Mcp-Protocol-Version": legacyProtocolVersion,
		"Mcp-Session-Id":       sessionID,
	}

	tests := []struct {
		name       string
		body       []byte
		wantStatus int
		wantText   string
	}{
		{
			name:       "unknown method",
			body:       []byte(`{"jsonrpc":"2.0","id":"unknown-method","method":"unknown/method","params":{}}`),
			wantStatus: http.StatusBadRequest,
			wantText:   `"unknown/method" unsupported`,
		},
		{
			name:       "malformed JSON",
			body:       []byte(`{"jsonrpc":"2.0",`),
			wantStatus: http.StatusBadRequest,
			wantText:   "malformed payload",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resp, body := rawMCPPost(t, runner, test.body, headers)

			require.Equal(t, test.wantStatus, resp.StatusCode, "response body: %s", body)
			if test.wantText != "" {
				assert.Contains(t, string(body), test.wantText)
				return
			}
		})
	}
}

func TestGeneratedServerRawMissingProtocolVersionFallsBackToLatestLegacy(t *testing.T) {
	t.Parallel()

	runner := startRawMCPServer(t)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "init-missing-version",
		"method":  "initialize",
		"params": map[string]any{
			"capabilities": map[string]any{},
			"clientInfo": map[string]any{
				"name":    "raw-contract-client",
				"version": "1.0.0",
			},
		},
	})
	require.NoError(t, err)

	resp, responseBody := rawMCPPost(t, runner, body, nil)

	require.Equal(t, http.StatusOK, resp.StatusCode, "response body: %s", responseBody)
	wire := decodeRawJSONRPCResponse(t, responseBody)
	require.Nil(t, wire.Error)
	assert.Equal(t, legacyProtocolVersion, wire.Result["protocolVersion"])
}

func TestGeneratedServerRawNotificationHasNoJSONRPCResponse(t *testing.T) {
	t.Parallel()

	runner := startRawMCPServer(t)
	sessionID := rawInitializeSession(t, runner)
	notification, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]any{},
	})
	require.NoError(t, err)

	resp, body := rawMCPPost(t, runner, notification, map[string]string{
		"Mcp-Protocol-Version": legacyProtocolVersion,
		"Mcp-Session-Id":       sessionID,
	})

	assert.Equal(t, http.StatusAccepted, resp.StatusCode, "response body: %s", body)
	assert.Empty(t, bytes.TrimSpace(body))
}
func TestGeneratedServerRawRejectsSSEBeforeInitialize(t *testing.T) {
	t.Parallel()

	runner := startRawMCPServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, runner.rpcURL(), nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, resp.Body.Close())
	}()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "response body: %s", body)
	assert.NotEqual(t, "text/event-stream", resp.Header.Get("Content-Type"))
	assert.Contains(t, string(body), "GET requires an Mcp-Session-Id header")
}

func TestGeneratedServerRawRejectsCallWithoutSession(t *testing.T) {
	t.Parallel()

	runner := startRawMCPServer(t)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "tools-before-init",
		"method":  "tools/list",
		"params":  map[string]any{},
	})
	require.NoError(t, err)

	resp, responseBody := rawMCPPost(t, runner, body, map[string]string{
		"Mcp-Protocol-Version": legacyProtocolVersion,
	})

	require.Equal(t, http.StatusOK, resp.StatusCode, "response body: %s", responseBody)
	wire := decodeRawJSONRPCResponse(t, responseBody)
	require.NotNil(t, wire.Error, "response body: %s; result: %#v", responseBody, wire.Result)
	assert.Contains(t, wire.Error.Message, "invalid during session initialization")
	if wire.Error.Code == 0 {
		t.Log("MCP Go SDK v1.8.0-pre.2 emits an untyped pre-initialization error before server middleware runs")
		return
	}
	assert.Equal(t, int64(-32602), wire.Error.Code)
}

func TestGeneratedServerRawRejectsDuplicateInitialize(t *testing.T) {
	t.Parallel()

	runner := startRawMCPServer(t)
	sessionID := rawInitializeSession(t, runner)
	resp, body := rawMCPPost(t, runner, rawInitializeRequest(t, "init-duplicate", legacyProtocolVersion), map[string]string{
		"Mcp-Protocol-Version": legacyProtocolVersion,
		"Mcp-Session-Id":       sessionID,
	})

	require.Equal(t, http.StatusOK, resp.StatusCode, "response body: %s", body)
	wire := decodeRawJSONRPCResponse(t, body)
	require.NotNil(t, wire.Error)
	assert.Equal(t, int64(-32602), wire.Error.Code)
	assert.Contains(t, wire.Error.Message, `duplicate "initialize"`)
}

func TestGeneratedServerRawReturnsToolValidationError(t *testing.T) {
	t.Parallel()

	runner := startRawMCPServer(t)
	sessionID := rawInitializedSession(t, runner)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "invalid-tool-arguments",
		"method":  "tools/call",
		"params": map[string]any{
			"name": "analyze_sentiment",
			"arguments": map[string]any{
				"text": 42,
			},
		},
	})
	require.NoError(t, err)

	resp, responseBody := rawMCPPost(t, runner, body, map[string]string{
		"Mcp-Protocol-Version": legacyProtocolVersion,
		"Mcp-Session-Id":       sessionID,
	})

	require.Equal(t, http.StatusOK, resp.StatusCode, "response body: %s", responseBody)
	wire := decodeRawJSONRPCResponse(t, responseBody)
	require.Nil(t, wire.Error)
	assert.Equal(t, true, wire.Result["isError"])
	content, ok := wire.Result["content"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, content)
}

func TestGeneratedServerRawRejectsDeniedResource(t *testing.T) {
	t.Parallel()

	runner := startRawMCPServer(t)
	sessionID := rawInitializedSession(t, runner)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "denied-resource",
		"method":  "resources/read",
		"params": map[string]any{
			"uri": "system://info",
		},
	})
	require.NoError(t, err)

	resp, responseBody := rawMCPPost(t, runner, body, map[string]string{
		"Mcp-Protocol-Version": legacyProtocolVersion,
		"Mcp-Session-Id":       sessionID,
		"x-mcp-deny-names":     "system_info",
	})

	require.Equal(t, http.StatusOK, resp.StatusCode, "response body: %s", responseBody)
	wire := decodeRawJSONRPCResponse(t, responseBody)
	require.NotNil(t, wire.Error, "response body: %s; result: %#v", responseBody, wire.Result)
	assert.Equal(t, int64(-32602), wire.Error.Code)
	assert.Contains(t, wire.Error.Message, "Resource URI is not allowed")
}

func TestGeneratedServerRawTerminatesSSEWithToolError(t *testing.T) {
	t.Parallel()

	runner := startRawMCPServer(t)
	sessionID := rawInitializedSession(t, runner)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "unknown-tool",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "does_not_exist",
			"arguments": map[string]any{},
		},
	})
	require.NoError(t, err)

	resp, responseBody := rawMCPPost(t, runner, body, map[string]string{
		"Mcp-Protocol-Version": legacyProtocolVersion,
		"Mcp-Session-Id":       sessionID,
	})

	require.Equal(t, http.StatusOK, resp.StatusCode, "response body: %s", responseBody)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	wire := decodeRawJSONRPCResponse(t, responseBody)
	require.NotNil(t, wire.Error)
	assert.Equal(t, int64(-32602), wire.Error.Code)
	assert.Contains(t, wire.Error.Message, "unknown tool")
}

func startRawMCPServer(t *testing.T) *Runner {
	t.Helper()

	if !SupportsServer() {
		t.Skip("integration fixture server is not available")
	}
	runner := NewRunner()
	runner.skipGeneration = true
	require.NoError(t, runner.startServer(t))
	t.Cleanup(runner.stopServer)
	return runner
}

func rawInitializeSession(t *testing.T, runner *Runner) string {
	t.Helper()

	resp, body := rawMCPPost(t, runner, rawInitializeRequest(t, "init-session", legacyProtocolVersion), nil)
	require.Equal(t, http.StatusOK, resp.StatusCode, "initialize response body: %s", body)
	sessionID := resp.Header.Get("Mcp-Session-Id")
	require.NotEmpty(t, sessionID)
	return sessionID
}

func rawInitializedSession(t *testing.T, runner *Runner) string {
	t.Helper()

	sessionID := rawInitializeSession(t, runner)
	notification, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]any{},
	})
	require.NoError(t, err)
	resp, body := rawMCPPost(t, runner, notification, map[string]string{
		"Mcp-Protocol-Version": legacyProtocolVersion,
		"Mcp-Session-Id":       sessionID,
	})
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "initialized response body: %s", body)
	require.Empty(t, bytes.TrimSpace(body))
	return sessionID
}

func rawInitializeRequest(t *testing.T, id, protocolVersion string) []byte {
	t.Helper()

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "raw-contract-client",
				"version": "1.0.0",
			},
		},
	})
	require.NoError(t, err)
	return body
}

func rawMCPPost(t *testing.T, runner *Runner, body []byte, headers map[string]string) (*rawHTTPResponse, []byte) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, runner.rpcURL(), bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		if strings.EqualFold(name, "Host") {
			req.Host = value
			continue
		}
		req.Header.Set(name, value)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return &rawHTTPResponse{StatusCode: resp.StatusCode, Header: resp.Header}, data
}

func decodeRawJSONRPCResponse(t *testing.T, body []byte) rawJSONRPCResponse {
	t.Helper()

	payload := body
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("event:")) {
		payload = nil
		for _, line := range strings.Split(string(body), "\n") {
			if after, ok := strings.CutPrefix(line, "data: "); ok {
				payload = []byte(after)
				break
			}
		}
		require.NotEmpty(t, payload, "SSE response has no data event: %s", body)
	}
	var wire rawJSONRPCResponse
	require.NoError(t, json.Unmarshal(payload, &wire), "response body: %s", body)
	return wire
}
