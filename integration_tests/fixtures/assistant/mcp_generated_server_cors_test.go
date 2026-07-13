package assistantapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpjsonrpcserver "example.com/assistant/gen/jsonrpc/mcp_assistant/server"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	mcpruntime "github.com/CaliLuke/loom-mcp/runtime/mcp"
	goahttp "github.com/CaliLuke/loom/http"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	allowedCORSOrigin    = "https://app.example.com"
	disallowedCORSOrigin = "https://evil.example.com"
)

func TestGeneratedSDKServerRuntimeCORSMatchesJSONRPC(t *testing.T) {
	policy := testRuntimeCORSPolicy(t)
	protection := http.NewCrossOriginProtection()
	protection.AddTrustedOrigin(allowedCORSOrigin)
	protection.AddTrustedOrigin(disallowedCORSOrigin)
	var requestContextCalls atomic.Int32
	sdkServer, err := mcpassistant.NewSDKServer(NewAssistant(), &mcpassistant.SDKServerOptions{
		PromptProvider: promptProvider{},
		RuntimeCORS:    &policy,
		RequestContext: func(ctx context.Context, _ *http.Request) context.Context {
			requestContextCalls.Add(1)
			return ctx
		},
		StreamableHTTP: &mcp.StreamableHTTPOptions{CrossOriginProtection: protection},
	})
	require.NoError(t, err)
	sdkHTTP := httptest.NewServer(sdkServer.Handler)
	defer sdkHTTP.Close()

	allowedPreflight := corsPreflight(t, sdkHTTP.URL+"/rpc", allowedCORSOrigin, http.MethodPost)
	require.Equal(t, http.StatusNoContent, allowedPreflight.StatusCode)
	assert.Equal(t, allowedCORSOrigin, allowedPreflight.Header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", allowedPreflight.Header.Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "600", allowedPreflight.Header.Get("Access-Control-Max-Age"))
	assert.Contains(t, allowedPreflight.Header.Get("Access-Control-Allow-Methods"), http.MethodPost)
	assert.Contains(t, allowedPreflight.Header.Get("Access-Control-Allow-Headers"), "Authorization")
	require.NoError(t, allowedPreflight.Body.Close())
	require.Zero(t, requestContextCalls.Load(), "preflight must complete before SDK request processing")

	disallowedPreflight := corsPreflight(t, sdkHTTP.URL+"/rpc", disallowedCORSOrigin, http.MethodPost)
	require.Equal(t, http.StatusNoContent, disallowedPreflight.StatusCode)
	assert.Empty(t, disallowedPreflight.Header.Get("Access-Control-Allow-Origin"))
	require.NoError(t, disallowedPreflight.Body.Close())
	require.Zero(t, requestContextCalls.Load(), "disallowed preflight must not enter SDK request processing")

	allowedInit := corsInitialize(t, sdkHTTP.URL+"/rpc", allowedCORSOrigin, "sdk-allowed")
	require.Equal(t, http.StatusOK, allowedInit.StatusCode)
	assert.Equal(t, allowedCORSOrigin, allowedInit.Header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", allowedInit.Header.Get("Access-Control-Allow-Credentials"))
	assert.Contains(t, allowedInit.Header.Get("Access-Control-Expose-Headers"), "Mcp-Session-Id")
	sessionID := allowedInit.Header.Get(mcpruntime.HeaderKeySessionID)
	require.NotEmpty(t, sessionID)
	_, err = io.Copy(io.Discard, allowedInit.Body)
	require.NoError(t, err)
	require.NoError(t, allowedInit.Body.Close())

	disallowedInit := corsInitialize(t, sdkHTTP.URL+"/rpc", disallowedCORSOrigin, "sdk-disallowed")
	require.Equal(t, http.StatusOK, disallowedInit.StatusCode)
	assert.Empty(t, disallowedInit.Header.Get("Access-Control-Allow-Origin"))
	_, err = io.Copy(io.Discard, disallowedInit.Body)
	require.NoError(t, err)
	require.NoError(t, disallowedInit.Body.Close())

	errorReq, err := http.NewRequest(http.MethodPost, sdkHTTP.URL+"/rpc", strings.NewReader(`{"not":"jsonrpc"}`))
	require.NoError(t, err)
	errorReq.Header.Set("Content-Type", "application/json")
	errorReq.Header.Set("Origin", allowedCORSOrigin)
	errorResp, err := sdkHTTP.Client().Do(errorReq)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, errorResp.StatusCode, http.StatusBadRequest)
	assert.Equal(t, allowedCORSOrigin, errorResp.Header.Get("Access-Control-Allow-Origin"))
	require.NoError(t, errorResp.Body.Close())

	sseCtx, cancelSSE := context.WithCancel(context.Background())
	sseReq, err := http.NewRequestWithContext(sseCtx, http.MethodGet, sdkHTTP.URL+"/rpc", nil)
	require.NoError(t, err)
	sseReq.Header.Set("Accept", "text/event-stream")
	sseReq.Header.Set("Origin", allowedCORSOrigin)
	sseReq.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
	sseResp, err := sdkHTTP.Client().Do(sseReq)
	require.NoError(t, err)
	assert.Equal(t, allowedCORSOrigin, sseResp.Header.Get("Access-Control-Allow-Origin"))
	cancelSSE()
	require.NoError(t, sseResp.Body.Close())

	jsonRPCPolicy := testRuntimeCORSPolicy(t)
	jsonRPCServer := newGeneratedJSONRPCServerWithCORSPolicy(t, jsonRPCPolicy)
	defer jsonRPCServer.Close()
	jsonRPCPreflight := corsPreflight(t, jsonRPCServer.URL+"/rpc", allowedCORSOrigin, http.MethodPost)
	require.Equal(t, allowedPreflight.StatusCode, jsonRPCPreflight.StatusCode)
	assert.Equal(t, allowedPreflight.Header.Get("Access-Control-Allow-Origin"), jsonRPCPreflight.Header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, allowedPreflight.Header.Get("Access-Control-Allow-Credentials"), jsonRPCPreflight.Header.Get("Access-Control-Allow-Credentials"))
	require.NoError(t, jsonRPCPreflight.Body.Close())
	jsonRPCInit := corsInitialize(t, jsonRPCServer.URL+"/rpc", allowedCORSOrigin, "jsonrpc-allowed")
	require.Equal(t, http.StatusOK, jsonRPCInit.StatusCode)
	assert.Equal(t, allowedInit.Header.Get("Access-Control-Allow-Origin"), jsonRPCInit.Header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, allowedInit.Header.Get("Access-Control-Allow-Credentials"), jsonRPCInit.Header.Get("Access-Control-Allow-Credentials"))
	require.NoError(t, jsonRPCInit.Body.Close())
}

func TestGeneratedSDKServerRuntimeCORSIsRequired(t *testing.T) {
	_, err := mcpassistant.NewSDKServer(NewAssistant(), &mcpassistant.SDKServerOptions{PromptProvider: promptProvider{}})
	require.EqualError(t, err, "runtime CORS policy is required by the MCP service design")
}

func newGeneratedJSONRPCServerWithCORSPolicy(t *testing.T, policy goahttp.RuntimeCORSPolicy) *httptest.Server {
	t.Helper()
	previousProtection := mcpjsonrpcserver.MCPCrossOriginProtection
	protection := http.NewCrossOriginProtection()
	protection.AddTrustedOrigin(allowedCORSOrigin)
	protection.AddTrustedOrigin(disallowedCORSOrigin)
	mcpjsonrpcserver.MCPCrossOriginProtection = protection
	t.Cleanup(func() {
		mcpjsonrpcserver.MCPCrossOriginProtection = previousProtection
	})
	endpoints := mcpassistant.NewEndpoints(NewMcpAssistant())
	mux := goahttp.NewMuxer()
	server := mcpjsonrpcserver.New(endpoints, mux, goahttp.RequestDecoder, goahttp.ResponseEncoder, func(context.Context, http.ResponseWriter, error) {}, policy)
	mcpjsonrpcserver.Mount(mux, server)
	return httptest.NewServer(mux)
}

func corsPreflight(t *testing.T, endpoint, origin, method string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodOptions, endpoint, nil)
	require.NoError(t, err)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", method)
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func corsInitialize(t *testing.T, endpoint, origin, id string) *http.Response {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcpassistant.DefaultProtocolVersion,
			"clientInfo":      map[string]any{"name": "cors-test", "version": "1.0.0"},
		},
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	require.NoError(t, err)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}
