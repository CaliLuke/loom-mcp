package assistantapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
	agentruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	"github.com/CaliLuke/loom-mcp/v2/runtime/mcp/sdkbridge"
	goahttp "github.com/CaliLuke/loom/http"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const testRequestStateKey = "0123456789abcdef0123456789abcdef"

type testHeaderRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func newGeneratedSDKServer(t *testing.T) (*mcpassistant.SDKServer, *httptest.Server) {
	return newGeneratedSDKServerWithAdapterOptions(t, nil)
}

func newGeneratedStatelessSDKServer(t *testing.T) (*mcpassistant.SDKServer, *httptest.Server) {
	t.Helper()

	corsPolicy := testRuntimeCORSPolicy(t)
	sdkServer, err := mcpassistant.NewSDKServer(NewAssistant(), &mcpassistant.SDKServerOptions{
		PromptProvider:  promptProvider{},
		RequestStateKey: []byte(testRequestStateKey),
		RuntimeCORS:     &corsPolicy,
		StreamableHTTP:  &sdkbridge.StreamableHTTPOptions{Stateless: true},
	})
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle("/rpc", sdkServer.Handler)
	mountOAuthDiscovery(mux, "/rpc")
	return sdkServer, httptest.NewServer(mux)
}

func newGeneratedSDKServerWithAdapterOptions(t *testing.T, adapterOpts *mcpassistant.MCPAdapterOptions) (*mcpassistant.SDKServer, *httptest.Server) {
	t.Helper()

	corsPolicy := testRuntimeCORSPolicy(t)
	sdkServer, err := mcpassistant.NewSDKServer(NewAssistant(), &mcpassistant.SDKServerOptions{
		PromptProvider:  promptProvider{},
		Adapter:         adapterOpts,
		RequestStateKey: []byte(testRequestStateKey),
		RuntimeCORS:     &corsPolicy,
		RequestContext: func(ctx context.Context, r *http.Request) context.Context {
			if r == nil {
				return ctx
			}
			if allow := r.Header.Get("x-mcp-allow-names"); allow != "" {
				ctx = mcpruntime.WithAllowedResourceNames(ctx, allow)
			}
			if deny := r.Header.Get("x-mcp-deny-names"); deny != "" {
				ctx = mcpruntime.WithDeniedResourceNames(ctx, deny)
			}
			if sessionID := r.Header.Get("x-fixture-session-id"); sessionID != "" {
				ctx = mcpruntime.WithProjectedToolCallMeta(ctx, agentruntime.ToolCallMeta{SessionID: sessionID})
			}
			return ctx
		},
	})
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle("/rpc", sdkServer.Handler)
	mountOAuthDiscovery(mux, "/rpc")
	return sdkServer, httptest.NewServer(mux)
}

func testRuntimeCORSPolicy(t *testing.T) goahttp.RuntimeCORSPolicy {
	t.Helper()
	policy, err := goahttp.NewRuntimeCORSPolicy(goahttp.CORSPolicy{Origins: []goahttp.CORSOrigin{{
		Pattern:     "https://app.example.com",
		Methods:     []string{http.MethodGet, http.MethodPost, http.MethodDelete},
		Headers:     []string{"Authorization", "Content-Type", "Mcp-Session-Id", "MCP-Protocol-Version"},
		Expose:      []string{"Mcp-Session-Id"},
		MaxAge:      600,
		Credentials: true,
	}}})
	require.NoError(t, err)
	return policy
}

func withTestRuntimeCORS(t *testing.T, opts *mcpassistant.SDKServerOptions) *mcpassistant.SDKServerOptions {
	t.Helper()
	if opts == nil {
		opts = new(mcpassistant.SDKServerOptions)
	}
	policy := testRuntimeCORSPolicy(t)
	opts.RuntimeCORS = &policy
	return opts
}

func mountOAuthDiscovery(mux *http.ServeMux, mountPath string) {
	mux.HandleFunc(mcpassistant.OAuthMetadataPath(mountPath), mcpassistant.HandleProtectedResourceMetadata)
	rootPath := mcpassistant.OAuthMetadataPath("")
	if rootPath != mcpassistant.OAuthMetadataPath(mountPath) {
		mux.HandleFunc(rootPath, mcpassistant.HandleProtectedResourceMetadata)
	}
}

func connectSDKSessionToServer(t *testing.T, rawURL string, headers map[string]string) *sdkmcp.ClientSession {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "fixture-sdk-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint: rawURL,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: testHeaderRoundTripper{
				base:    newTestHTTPTransport(t),
				headers: headers,
			},
		},
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	return session
}

func newTestHTTPTransport(t *testing.T) *http.Transport {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	return transport
}

func (rt testHeaderRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := rt.base
	if base == nil {
		base = http.DefaultTransport
	}
	cloned := req.Clone(req.Context())
	for key, value := range rt.headers {
		cloned.Header.Set(key, value)
	}
	return base.RoundTrip(cloned)
}
