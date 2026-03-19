package assistantapi

import (
	"bufio"
	"bytes"
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
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mcpruntime "goa.design/goa-ai/runtime/mcp"
	goahttp "goa.design/goa/v3/http"
)

func TestGeneratedNewCallerAgainstGeneratedServerNormalizesMultiContent(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServer(t)
	defer server.Close()

	caller := newGeneratedCallerFromServer(t, server.URL)

	textResp, err := caller.CallTool(context.Background(), mcpruntime.CallRequest{
		Tool:    "multi_content",
		Payload: json.RawMessage(`{"count":2}`),
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"result":"hello world!"}`, string(textResp.Result))

	imageResp, err := caller.CallTool(context.Background(), mcpruntime.CallRequest{
		Tool:    "multi_content",
		Payload: json.RawMessage(`{"count":4}`),
	})
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(imageResp.Result, &result))
	assert.Equal(t, "image", result["type"])
	assert.Equal(t, "image/png", result["mimeType"])
}

func TestGeneratedAdapterAgainstGeneratedServerReturnsRetryPrompt(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServer(t)
	defer server.Close()

	out, err := newAdapterEndpoints(t, server).ExecuteCode(context.Background(), invalidExecuteCodePayload())
	require.Nil(t, out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Redo the operation now with valid parameters.")
	assert.Contains(t, err.Error(), `"enum":["python","javascript"]`)
}

func TestGeneratedCallerMatchesRuntimeHTTPCaller(t *testing.T) {
	t.Parallel()

	jsonrpcServer := newGeneratedJSONRPCServer(t)
	defer jsonrpcServer.Close()

	_, sdkHTTPServer := newGeneratedSDKServer(t)
	defer sdkHTTPServer.Close()

	generatedCaller := newGeneratedCallerFromServer(t, jsonrpcServer.URL)
	runtimeSession := connectSDKSessionToServer(t, sdkHTTPServer.URL+"/rpc", nil)
	runtimeCaller := mcpruntime.NewSessionCaller(runtimeSession, nil)
	defer func() {
		require.NoError(t, runtimeCaller.Close())
	}()

	req := mcpruntime.CallRequest{
		Tool:    "analyze_sentiment",
		Payload: json.RawMessage(`{"text":"I love parity checks"}`),
	}

	generatedResp, err := generatedCaller.CallTool(context.Background(), req)
	require.NoError(t, err)

	runtimeResp, err := runtimeCaller.CallTool(context.Background(), req)
	require.NoError(t, err)

	require.JSONEq(t, string(generatedResp.Result), string(runtimeResp.Result))
	require.JSONEq(t, string(generatedResp.Structured), string(runtimeResp.Structured))

}

func TestGeneratedJSONRPCServerEventsStreamPublishesNotifications(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID, err := initializeJSONRPCSession(ctx, server.URL)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/rpc", nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)

	resultCh := make(chan string, 1)
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
		resultCh <- readSSEData(resp.Body)
	}()

	message := "status from generated sdk server"
	notifyReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      "notify-1",
		"method":  "notify_status_update",
		"params": map[string]any{
			"type":    "info",
			"message": message,
		},
	}
	err = postJSONRPC(ctx, server.URL+"/rpc", sessionID, notifyReq)
	require.NoError(t, err)

	select {
	case data := <-resultCh:
		assert.NotContains(t, data, "ERROR:")
		assert.NotContains(t, data, "STATUS:")
		assert.Contains(t, data, `"method":"events/stream"`)
		assert.Contains(t, data, message)
	case <-ctx.Done():
		t.Fatal("timed out waiting for events/stream notification")
	}
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

func newGeneratedJSONRPCServer(t *testing.T) *httptest.Server {
	t.Helper()

	svc := NewMcpAssistant()
	endpoints := mcpassistant.NewEndpoints(svc)
	mux := goahttp.NewMuxer()
	server := mcpAssistantjssvr.New(
		endpoints,
		mux,
		goahttp.RequestDecoder,
		goahttp.ResponseEncoder,
		func(context.Context, http.ResponseWriter, error) {},
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
	return sdkServer, httptest.NewServer(mux)
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

type testHeaderRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
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

type testSessionDoer struct {
	base      *http.Client
	sessionID string
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
	req.Body = io.NopCloser(bytes.NewReader(body))
	var envelope struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ""
	}
	return envelope.Method
}
