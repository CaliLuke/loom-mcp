package assistantapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	assistant "example.com/assistant/gen/assistant"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// TestRequestContextSeesPerCallHeaders pins the gap autok flagged: when a
// client sends a distinct X-Request-ID header on each MCP HTTP call
// (initialize, then tools/call), the generated server's RequestContext
// callback must observe the LIVE per-call header on the tool call — not the
// stale value from the initialize request.
//
// This regression is the release blocker the autok review raised. The
// header-precedence patch in sdk_server_file.go fixes the contention
// between extra.Header (per-JSON-RPC call) and the ctx headers stored in
// `mcpruntime.WithRequestHeaders`; this test exercises the runtime contract
// against the real generated SDK server.
func TestRequestContextSeesPerCallHeaders(t *testing.T) {
	t.Parallel()

	const headerName = "X-Request-ID"
	var (
		mu       sync.Mutex
		captured = map[string][]string{} // method → request ids seen by RequestContext
	)
	captureRequestID := func(method, value string) {
		mu.Lock()
		defer mu.Unlock()
		captured[method] = append(captured[method], value)
	}
	snapshot := func() map[string][]string {
		mu.Lock()
		defer mu.Unlock()
		out := make(map[string][]string, len(captured))
		for key, values := range captured {
			out[key] = append([]string(nil), values...)
		}
		return out
	}

	// Use the application hook exposed by the generated server and bucket each
	// invocation by the method stamped on the real inbound HTTP request.
	sdkServer, err := mcpassistant.NewSDKServer(NewAssistant(), withTestRuntimeCORS(t, &mcpassistant.SDKServerOptions{
		PromptProvider: promptProvider{},
		RequestContext: func(ctx context.Context, r *http.Request) context.Context {
			if r == nil {
				return ctx
			}
			rid := r.Header.Get(headerName)
			method := r.Header.Get("X-Test-Method")
			if rid != "" && method != "" {
				captureRequestID(method, rid)
			}
			return ctx
		},
	}))
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.Handle("/rpc", sdkServer.Handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Per-call round tripper assigns a unique X-Request-ID and stamps the
	// JSON-RPC method on a side-channel header the RequestContext callback
	// reads back. We can read the JSON-RPC body to know which method this
	// HTTP request carries.
	rt := newPerCallStampingRoundTripper(http.DefaultTransport)

	client := mcp.NewClient(&mcp.Implementation{Name: "request-id-client", Version: "1.0.0"}, nil)
	connectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := client.Connect(connectCtx, &mcp.StreamableClientTransport{
		Endpoint:             srv.URL + "/rpc",
		HTTPClient:           &http.Client{Timeout: 5 * time.Second, Transport: rt},
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	callCtx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	res, err := session.CallTool(callCtx, &mcp.CallToolParams{
		Name:      "analyze_sentiment",
		Arguments: map[string]any{"text": "hello"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.Content)

	got := snapshot()
	require.Len(t, got["initialize"], 1, "RequestContext must run once for initialize")
	require.Len(t, got["tools/call"], 1, "RequestContext must run once for tools/call")
	initID := got["initialize"][0]
	callID := got["tools/call"][0]
	require.NotEqual(t, initID, callID, "tools/call must see its own X-Request-ID, not initialize's stale value (gap #2 from the autok review)")
}

// perCallStampingRoundTripper assigns a unique X-Request-ID to each HTTP
// request and stamps a parallel X-Test-Method header so the
// RequestContext callback can bucket captured ids by JSON-RPC method
// without having to parse the request body.
type perCallStampingRoundTripper struct {
	base http.RoundTripper
	mu   sync.Mutex
	n    int
}

func newPerCallStampingRoundTripper(base http.RoundTripper) *perCallStampingRoundTripper {
	return &perCallStampingRoundTripper{base: base}
}

func (r *perCallStampingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.n++
	id := fmt.Sprintf("req-%d", r.n)
	r.mu.Unlock()

	clone := req.Clone(req.Context())
	clone.Header.Set("X-Request-ID", id)
	if method, err := readJSONRPCMethod(req); err == nil && method != "" {
		clone.Header.Set("X-Test-Method", method)
	}
	return r.base.RoundTrip(clone)
}

// readJSONRPCMethod peeks the JSON-RPC method without consuming the body.
// It clones the request body via GetBody when available; the MCP SDK
// always populates GetBody for its POST requests.
func readJSONRPCMethod(req *http.Request) (string, error) {
	if req == nil || req.GetBody == nil {
		return "", nil
	}
	body, err := req.GetBody()
	if err != nil {
		return "", err
	}
	defer body.Close()
	buf := make([]byte, 256)
	n, _ := body.Read(buf)
	chunk := string(buf[:n])
	// Cheap method extraction: look for `"method":"<name>"`.
	idx := strings.Index(chunk, `"method":`)
	if idx < 0 {
		return "", nil
	}
	rest := chunk[idx+len(`"method":`):]
	start := strings.Index(rest, `"`)
	if start < 0 {
		return "", nil
	}
	rest = rest[start+1:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return "", nil
	}
	return rest[:end], nil
}

// _ keeps the assistant import live for environments where the generated
// service interface drifts; the import is required for NewAssistant to be
// resolvable across regeneration cycles of the assistant fixture.
var _ = assistant.Service(nil)
