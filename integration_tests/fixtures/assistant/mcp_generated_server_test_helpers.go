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
	"testing"
	"time"

	assistant "example.com/assistant/gen/assistant"
	mcpAssistantjsonrpcc "example.com/assistant/gen/jsonrpc/mcp_assistant/client"
	mcpAssistantjssvr "example.com/assistant/gen/jsonrpc/mcp_assistant/server"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	mcpruntime "github.com/CaliLuke/loom-mcp/runtime/mcp"
	goahttp "github.com/CaliLuke/loom/http"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type rawEventsStream struct {
	resultCh chan string
	cancel   context.CancelFunc
}

type testHeaderRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

type testSessionDoer struct {
	base      *http.Client
	sessionID string
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
	t.Helper()

	svc := NewMcpAssistantWithOptions(&mcpassistant.MCPAdapterOptions{
		Logger: func(ctx context.Context, event string, details any) {
			t.Helper()
			t.Logf("generated-mcp-adapter event=%s details=%v session_id=%s", event, details, mcpruntime.SessionIDFromContext(ctx))
		},
	})
	endpoints := mcpassistant.NewEndpoints(svc)
	mux := goahttp.NewMuxer()
	server := mcpAssistantjssvr.New(
		endpoints,
		mux,
		goahttp.RequestDecoder,
		goahttp.ResponseEncoder,
		func(ctx context.Context, _ http.ResponseWriter, err error) {
			t.Helper()
			t.Logf("generated-jsonrpc-server err=%v session_id=%s", err, mcpruntime.SessionIDFromContext(ctx))
		},
	)
	mcpAssistantjssvr.Mount(mux, server)
	return httptest.NewServer(mux)
}

func newGeneratedSDKServer(t *testing.T) (*mcpassistant.SDKServer, *httptest.Server) {
	t.Helper()

	sdkServer, err := mcpassistant.NewSDKServer(NewAssistant(), &mcpassistant.SDKServerOptions{
		PromptProvider: promptProvider{},
		RequestContext: func(ctx context.Context, r *http.Request) context.Context {
			if r == nil {
				return ctx
			}
			if allow := r.Header.Get("x-mcp-allow-names"); allow != "" {
				ctx = context.WithValue(ctx, "mcp_allow_names", allow)
			}
			if deny := r.Header.Get("x-mcp-deny-names"); deny != "" {
				ctx = context.WithValue(ctx, "mcp_deny_names", deny)
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

	doer := &testSessionDoer{
		base: &http.Client{
			Timeout: 10 * time.Second,
			Transport: testHeaderRoundTripper{
				base: http.DefaultTransport,
				headers: map[string]string{
					"Accept": "text/event-stream",
				},
			},
		},
	}
	client := mcpAssistantjsonrpcc.NewClient(
		u.Scheme,
		u.Host,
		doer,
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
				base:    http.DefaultTransport,
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
				base:    http.DefaultTransport,
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

func (d *testSessionDoer) Do(req *http.Request) (*http.Response, error) {
	if d.base == nil {
		d.base = &http.Client{Timeout: 10 * time.Second}
	}
	method := jsonRPCMethodName(req)
	if method != "initialize" && d.sessionID != "" {
		req.Header.Set(mcpruntime.HeaderKeySessionID, d.sessionID)
	}
	resp, err := d.base.Do(req)
	if err != nil {
		return nil, err
	}
	if sessionID := resp.Header.Get(mcpruntime.HeaderKeySessionID); sessionID != "" {
		d.sessionID = sessionID
	}
	return resp, nil
}

func jsonRPCMethodName(req *http.Request) string {
	if req == nil || req.Body == nil {
		return ""
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return ""
	}
	req.Body = io.NopCloser(strings.NewReader(string(body)))
	var envelope struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	return envelope.Method
}
