package assistantapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	assistant "example.com/assistant/gen/assistant"
	mcpAssistantjsonrpcc "example.com/assistant/gen/jsonrpc/mcp_assistant/client"
	mcpAssistantjssvr "example.com/assistant/gen/jsonrpc/mcp_assistant/server"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	goahttp "github.com/CaliLuke/loom/http"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

const testRequestStateKey = "0123456789abcdef0123456789abcdef"

type rawEventsStream struct {
	resultCh chan string
	cancel   context.CancelFunc
}

type subscriptionReadyBroadcaster struct {
	mcpruntime.Broadcaster
	ready chan struct{}
	once  sync.Once
}

func newSubscriptionReadyBroadcaster() *subscriptionReadyBroadcaster {
	return &subscriptionReadyBroadcaster{
		Broadcaster: mcpruntime.NewChannelBroadcaster(32, true),
		ready:       make(chan struct{}),
	}
}

func (b *subscriptionReadyBroadcaster) Subscribe(ctx context.Context) (mcpruntime.Subscription, error) {
	sub, err := b.Broadcaster.Subscribe(ctx)
	if err == nil {
		b.once.Do(func() { close(b.ready) })
	}
	return sub, err
}

func (b *subscriptionReadyBroadcaster) SubscribeSession(ctx context.Context, sessionID string) (mcpruntime.Subscription, error) {
	scoped, ok := b.Broadcaster.(mcpruntime.SessionBroadcaster)
	if !ok {
		return nil, fmt.Errorf("broadcaster does not support session subscriptions")
	}
	sub, err := scoped.SubscribeSession(ctx, sessionID)
	if err == nil {
		b.once.Do(func() { close(b.ready) })
	}
	return sub, err
}

func (b *subscriptionReadyBroadcaster) PublishSession(sessionID string, event any) {
	scoped, ok := b.Broadcaster.(mcpruntime.SessionBroadcaster)
	if !ok {
		return
	}
	scoped.PublishSession(sessionID, event)
}

type testHeaderRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func initializeJSONRPCSession(ctx context.Context, rawURL string) (string, error) {
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      "init-1",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"clientInfo": map[string]any{
				"name":    "events-e2e",
				"version": "1.0.0",
			},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL+"/rpc", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("initialize returned status %d", resp.StatusCode)
	}
	sessionID := resp.Header.Get(mcpruntime.HeaderKeySessionID)
	if sessionID == "" {
		return "", fmt.Errorf("missing %s header", mcpruntime.HeaderKeySessionID)
	}
	return sessionID, nil
}

func postJSONRPC(ctx context.Context, endpoint string, sessionID string, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
		req.Header.Set(mcpruntime.HeaderKeyProtocolVersion, mcpassistant.DefaultProtocolVersion)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("json-rpc returned status %d", resp.StatusCode)
	}
	return nil
}

func openRawEventsStream(t *testing.T, ctx context.Context, server *httptest.Server, sessionID string) *rawEventsStream {
	t.Helper()

	streamCtx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, server.URL+"/rpc", nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
	req.Header.Set(mcpruntime.HeaderKeyProtocolVersion, mcpassistant.DefaultProtocolVersion)

	resultCh := make(chan string, 1)
	readyCh := make(chan struct{})
	go func() {
		resp, err := server.Client().Do(req)
		if err != nil {
			resultCh <- "ERROR: " + err.Error()
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			resultCh <- fmt.Sprintf("STATUS: %d", resp.StatusCode)
			return
		}
		close(readyCh)
		resultCh <- readSSEData(resp.Body)
	}()

	select {
	case <-readyCh:
		return &rawEventsStream{resultCh: resultCh, cancel: cancel}
	case data := <-resultCh:
		cancel()
		t.Fatalf("stream did not become ready: %s", data)
	case <-ctx.Done():
		cancel()
		t.Fatal("timed out waiting for event stream to connect")
	}

	return nil
}

func newGeneratedJSONRPCServer(t *testing.T) *httptest.Server {
	return newGeneratedJSONRPCServerWithAdapterOptions(t, nil)
}

func newGeneratedJSONRPCServerWithAdapterOptions(t *testing.T, opts *mcpassistant.MCPAdapterOptions) *httptest.Server {
	t.Helper()

	if opts == nil {
		opts = &mcpassistant.MCPAdapterOptions{}
	}
	opts.Logger = func(ctx context.Context, event string, details any) {
		t.Helper()
		t.Logf("generated-mcp-adapter event=%s details=%v session_id=%s", event, details, mcpruntime.SessionIDFromContext(ctx))
	}
	svc := NewMcpAssistantWithOptions(opts)
	endpoints := mcpassistant.NewEndpoints(svc)
	mux := goahttp.NewMuxer()
	corsPolicy := testRuntimeCORSPolicy(t)
	server := mcpAssistantjssvr.New(
		endpoints,
		mux,
		goahttp.RequestDecoder,
		goahttp.ResponseEncoder,
		func(ctx context.Context, _ http.ResponseWriter, err error) {
			t.Helper()
			t.Logf("generated-jsonrpc-server err=%v session_id=%s", err, mcpruntime.SessionIDFromContext(ctx))
		},
		corsPolicy,
	)
	mcpAssistantjssvr.Mount(mux, server)
	return httptest.NewServer(mux)
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
		StreamableHTTP:  &sdkmcp.StreamableHTTPOptions{Stateless: true},
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

// mountOAuthDiscovery wires the generated OAuth protected-resource
// metadata handler onto the provided mux at both the path-suffixed
// well-known URL (per RFC 9728 §3.1) and the root alias.
func mountOAuthDiscovery(mux *http.ServeMux, mountPath string) {
	mux.HandleFunc(mcpassistant.OAuthMetadataPath(mountPath), mcpassistant.HandleProtectedResourceMetadata)
	rootPath := mcpassistant.OAuthMetadataPath("")
	if rootPath != mcpassistant.OAuthMetadataPath(mountPath) {
		mux.HandleFunc(rootPath, mcpassistant.HandleProtectedResourceMetadata)
	}
}

func newGeneratedCallerFromServer(t *testing.T, rawURL string) mcpruntime.Caller {
	t.Helper()

	u, err := url.Parse(rawURL)
	require.NoError(t, err)

	// The generated JSON-RPC client owns the MCP transport obligations
	// (Accept header, Mcp-Session-Id capture/replay, protocol version), so a
	// plain HTTP client suffices here.
	client := mcpAssistantjsonrpcc.NewClient(
		u.Scheme,
		u.Host,
		&http.Client{Timeout: 10 * time.Second},
		goahttp.RequestEncoder,
		goahttp.ResponseDecoder,
		false,
	)
	_, err = client.Initialize()(context.Background(), &mcpassistant.InitializePayload{
		ProtocolVersion: "2025-06-18",
		ClientInfo: &mcpassistant.ClientInfo{
			Name:    "generated-caller-e2e",
			Version: "1.0.0",
		},
	})
	require.NoError(t, err)

	return mcpAssistantjsonrpcc.NewCaller(client, "assistant-mcp")
}

func newGeneratedJSONRPCTransportClient(t *testing.T, rawURL string, headers map[string]string) *mcpAssistantjsonrpcc.Client {
	t.Helper()

	u, err := url.Parse(rawURL)
	require.NoError(t, err)

	return mcpAssistantjsonrpcc.NewClient(
		u.Scheme,
		u.Host,
		&http.Client{
			Timeout: 10 * time.Second,
			Transport: testHeaderRoundTripper{
				base:    newTestHTTPTransport(t),
				headers: headers,
			},
		},
		goahttp.RequestEncoder,
		goahttp.ResponseDecoder,
		false,
	)
}

func connectSDKSessionToServer(t *testing.T, rawURL string, headers map[string]string) *sdkmcp.ClientSession {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "fixture-sdk-client",
		Version: "1.0.0",
	}, nil)
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

func readSSEData(body io.Reader) string {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			return strings.TrimPrefix(line, "data: ")
		}
	}
	return ""
}

func invalidExecuteCodePayload() *assistant.ExecuteCodePayload {
	return &assistant.ExecuteCodePayload{
		Language: "ruby",
		Code:     "puts 1",
	}
}

func (s *rawEventsStream) Close() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
}

func (s *rawEventsStream) Result() <-chan string {
	return s.resultCh
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
