package assistantapi

import (
	"context"
	"encoding/json/jsontext"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const generatedCallerHelperEnv = "LOOM_MCP_GENERATED_CALLER_HELPER"

func TestRuntimeCallersCompleteGeneratedElicitation(t *testing.T) {
	for _, transport := range []string{"http", "http_legacy", "sse", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			caller := newGeneratedRuntimeCaller(t, transport, multiStepElicitationHandler(t), 0)
			t.Cleanup(func() {
				assert.NoError(t, caller.Close())
			})
			result, err := caller.CallTool(t.Context(), mcpruntime.CallRequest{
				Tool: "summarize_text", Payload: jsontext.Value(`{"text":"needs-multi-elicitation"}`),
			})
			require.NoError(t, err)
			assert.JSONEq(t, `{"summary":"Protocol migration complete for SDK maintainers."}`, string(result.Result))
		})
	}
}

func TestRuntimeCallerWithoutElicitationHandler(t *testing.T) {
	caller := newGeneratedRuntimeCaller(t, "http", nil, 0)
	t.Cleanup(func() {
		assert.NoError(t, caller.Close())
	})
	_, err := caller.CallTool(t.Context(), mcpruntime.CallRequest{
		Tool: "summarize_text", Payload: jsontext.Value(`{"text":"needs-elicitation"}`),
	})
	require.Error(t, err)
}

func TestRuntimeStdioCallerFrameLimit(t *testing.T) {
	for _, test := range []struct {
		name      string
		limit     int
		wantError bool
	}{
		{name: "bounded", limit: 32 * 1024, wantError: true},
		{name: "default"},
		{name: "unlimited", limit: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			summary := strings.Repeat("a", 64*1024)
			caller := newGeneratedRuntimeCaller(t, "stdio", func(context.Context, *sdkmcp.ElicitRequest) (*sdkmcp.ElicitResult, error) {
				return &sdkmcp.ElicitResult{Action: "accept", Content: map[string]any{"summary": summary}}, nil
			}, test.limit)
			t.Cleanup(func() {
				err := caller.Close()
				if test.wantError && err != nil {
					// Closing after a framing failure may terminate a server still
					// writing the oversized response to its stdout pipe.
					var exitErr *exec.ExitError
					assert.ErrorAs(t, err, &exitErr)
					return
				}
				assert.NoError(t, err)
			})
			result, err := caller.CallTool(t.Context(), mcpruntime.CallRequest{
				Tool: "summarize_text", Payload: jsontext.Value(`{"text":"needs-elicitation"}`),
			})
			if test.wantError {
				require.ErrorContains(t, err, "maximum line length")
				return
			}
			require.NoError(t, err)
			assert.JSONEq(t, `{"summary":"`+summary+`"}`, string(result.Result))
		})
	}
}

func TestRuntimeHTTPCallerSurvivesFailedRequest(t *testing.T) {
	server, httpServer := newGeneratedStatelessSDKServer(t)
	httpServer.Close()
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Mcp-Method") == "tools/call" && calls.Add(1) == 1 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		server.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(gateway.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	caller, err := mcpruntime.NewHTTPCaller(ctx, mcpruntime.HTTPOptions{Endpoint: gateway.URL, Client: gateway.Client()})
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, caller.Close())
	})
	request := mcpruntime.CallRequest{Tool: "summarize_text", Payload: jsontext.Value(`{"text":"hello"}`)}
	_, err = caller.CallTool(ctx, request)
	require.ErrorContains(t, err, "Service Unavailable")
	result, err := caller.CallTool(ctx, request)
	require.NoError(t, err, "a failed stateless HTTP request must not break subsequent calls")
	assert.JSONEq(t, `{"summary":"Implementation handoff generated."}`, string(result.Result))
	assert.Equal(t, int32(2), calls.Load())
}

func TestGeneratedCallerStdioProcess(t *testing.T) {
	if os.Getenv(generatedCallerHelperEnv) != "1" {
		t.Skip("helper subprocess")
	}
	server, err := mcpassistant.NewSDKServer(NewAssistant(), withTestRuntimeCORS(t, &mcpassistant.SDKServerOptions{
		PromptProvider: promptProvider{}, RequestStateKey: []byte(testRequestStateKey),
	}))
	require.NoError(t, err)
	require.NoError(t, server.Server.Run(context.Background(), &sdkmcp.StdioTransport{}))
	os.Exit(0)
}

func newGeneratedRuntimeCaller(t *testing.T, transport string, handler func(context.Context, *sdkmcp.ElicitRequest) (*sdkmcp.ElicitResult, error), maxLineLength int) *mcpruntime.SessionCaller {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	var caller *mcpruntime.SessionCaller
	var err error
	if transport == "stdio" {
		caller, err = mcpruntime.NewStdioCaller(ctx, mcpruntime.StdioOptions{
			Command: os.Args[0], Args: []string{"-test.run=^TestGeneratedCallerStdioProcess$"},
			Env: append(os.Environ(), generatedCallerHelperEnv+"=1"), InitTimeout: 10 * time.Second,
			ElicitationHandler: handler, MaxLineLength: maxLineLength,
		})
	} else {
		server, httpServer := newGeneratedStatelessSDKServer(t)
		switch transport {
		case "http_legacy":
			httpServer.Close()
			server, httpServer = newGeneratedSDKServer(t)
		case "sse":
			httpServer.Close()
			httpServer = httptest.NewServer(sdkmcp.NewSSEHandler(func(*http.Request) *sdkmcp.Server {
				return server.Server
			}, nil))
		}
		t.Cleanup(httpServer.Close)
		opts := mcpruntime.HTTPOptions{Endpoint: httpServer.URL + "/rpc", Client: httpServer.Client(), InitTimeout: 10 * time.Second, ElicitationHandler: handler}
		if transport == "sse" {
			opts.Endpoint = httpServer.URL
			caller, err = mcpruntime.NewSSECaller(ctx, opts)
		} else {
			caller, err = mcpruntime.NewHTTPCaller(ctx, opts)
		}
	}
	require.NoError(t, err)
	return caller
}
