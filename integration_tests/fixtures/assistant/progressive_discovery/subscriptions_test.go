package progressivediscovery

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpcatalog "example.com/assistant/progressive_discovery/gen/mcp_catalog"
	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedSDKServerResourceSubscriptionLifecycle(t *testing.T) {
	updates := make(chan string, 2)
	server, err := mcpcatalog.NewSDKServer(NewCatalog(), nil)
	require.NoError(t, err)
	httpServer := httptest.NewServer(server.Handler)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "subscription-test", Version: "1.0.0"}, &mcp.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, request *mcp.ResourceUpdatedNotificationRequest) {
			updates <- request.Params.URI
		},
	})
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, session.Close()) }()

	require.Error(t, session.Subscribe(ctx, &mcp.SubscribeParams{URI: "status://unknown"}))
	require.Error(t, server.ResourceUpdated(ctx, "status://unknown"))
	require.NoError(t, session.Subscribe(ctx, &mcp.SubscribeParams{URI: "status://current"}))
	require.NoError(t, server.ResourceUpdated(ctx, "status://current"))
	select {
	case uri := <-updates:
		assert.Equal(t, "status://current", uri)
	case <-ctx.Done():
		t.Fatal("timed out waiting for resource update")
	}

	require.NoError(t, session.Unsubscribe(ctx, &mcp.UnsubscribeParams{URI: "status://current"}))
	require.NoError(t, server.ResourceUpdated(ctx, "status://current"))
	select {
	case uri := <-updates:
		t.Fatalf("received update after unsubscribe: %s", uri)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestGeneratedSDKServerSSEContainsOnlyJSONRPCEvents(t *testing.T) {
	server, err := mcpcatalog.NewSDKServer(NewCatalog(), nil)
	require.NoError(t, err)
	httpServer := httptest.NewServer(server.Handler)
	t.Cleanup(httpServer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The affected transport was the persistent stateful SSE stream. MCP
	// 2026-07-28 is stateless, so negotiate the latest stateful protocol here.
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"strict-sse-test","version":"1.0.0"}}}`
	initReq, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL, strings.NewReader(initialize))
	require.NoError(t, err)
	initReq.Header.Set("Content-Type", "application/json")
	initReq.Header.Set("Accept", "application/json, text/event-stream")
	initResp, err := http.DefaultClient.Do(initReq)
	require.NoError(t, err)
	initBody, err := io.ReadAll(initResp.Body)
	require.NoError(t, err)
	require.NoError(t, initResp.Body.Close())
	require.Less(t, initResp.StatusCode, http.StatusBadRequest, string(initBody))
	sessionID := initResp.Header.Get(mcpruntime.HeaderKeySessionID)
	require.NotEmpty(t, sessionID)

	post := func(body string) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
		req.Header.Set(mcpruntime.HeaderKeyProtocolVersion, "2025-06-18")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		responseBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Less(t, resp.StatusCode, http.StatusBadRequest, string(responseBody))
	}
	post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	post(`{"jsonrpc":"2.0","id":2,"method":"resources/subscribe","params":{"uri":"status://current"}}`)

	streamReq, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL, nil)
	require.NoError(t, err)
	streamReq.Header.Set("Accept", "text/event-stream")
	streamReq.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
	streamReq.Header.Set(mcpruntime.HeaderKeyProtocolVersion, "2025-06-18")
	streamResp, err := http.DefaultClient.Do(streamReq)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, streamResp.Body.Close()) })
	require.Equal(t, http.StatusOK, streamResp.StatusCode)

	scanner := bufio.NewScanner(streamResp.Body)
	openingFrame := readSSEFrame(t, scanner)
	require.Len(t, openingFrame, 1)
	require.True(t, strings.HasPrefix(openingFrame[0], ":"), "first SSE frame is not a comment: %q", openingFrame)
	for range 2 {
		require.NoError(t, server.ResourceUpdated(ctx, "status://current"))
		frame := readSSEFrame(t, scanner)
		for _, line := range frame {
			require.False(t, strings.HasPrefix(line, "retry:"), "unexpected retry control field in SSE frame %q", frame)
			require.NotEqual(t, "event: retry", line, "unexpected retry control event in SSE frame %q", frame)
		}
		data := sseFrameData(t, frame)
		var message struct {
			JSONRPC string `json:"jsonrpc"`
			Method  string `json:"method"`
		}
		require.NoError(t, json.Unmarshal([]byte(data), &message))
		assert.Equal(t, "2.0", message.JSONRPC)
		assert.Equal(t, "notifications/resources/updated", message.Method)
	}
}

func TestGeneratedSDKServerRejectsStatelessWatchableResources(t *testing.T) {
	_, err := mcpcatalog.NewSDKServer(NewCatalog(), &mcpcatalog.SDKServerOptions{
		StreamableHTTP: &mcp.StreamableHTTPOptions{Stateless: true},
	})
	require.ErrorContains(t, err, "watchable MCP resources require stateful Streamable HTTP sessions")
}

func TestGeneratedSDKServerAggregatesLoomStreamingToolResults(t *testing.T) {
	server, err := mcpcatalog.NewSDKServer(NewCatalog(), nil)
	require.NoError(t, err)
	httpServer := httptest.NewServer(server.Handler)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "stream-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, session.Close()) }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "stream_chunks"})
	require.NoError(t, err)
	require.Len(t, result.Content, 2)
	first, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	second, ok := result.Content[1].(*mcp.TextContent)
	require.True(t, ok)
	assert.JSONEq(t, `{"chunk":"first"}`, first.Text)
	assert.JSONEq(t, `{"chunk":"second"}`, second.Text)
}

func TestGeneratedSDKServerPropagatesClientCancellation(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	service := &catalogService{cancelStarted: started, cancelSeen: canceled}
	server, err := mcpcatalog.NewSDKServer(service, nil)
	require.NoError(t, err)
	httpServer := httptest.NewServer(server.Handler)
	t.Cleanup(httpServer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "cancellation-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL, DisableStandaloneSSE: true}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })

	callCtx, cancelCall := context.WithCancel(ctx)
	callResult := make(chan error, 1)
	go func() {
		_, callErr := session.CallTool(callCtx, &mcp.CallToolParams{Name: "wait_for_cancel"})
		callResult <- callErr
	}()

	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("service did not start the cancellable tool")
	}
	cancelCall()
	require.Error(t, <-callResult)
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("SDK cancellation did not reach the service context")
	}
}

func readSSEFrame(t *testing.T, scanner *bufio.Scanner) []string {
	t.Helper()
	var lines []string
	for scanner.Scan() {
		if scanner.Text() == "" {
			return lines
		}
		lines = append(lines, scanner.Text())
	}
	require.NoError(t, scanner.Err())
	t.Fatal("SSE stream ended before the next frame")
	return nil
}

func sseFrameData(t *testing.T, frame []string) string {
	t.Helper()
	var data []string
	for _, line := range frame {
		if value, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(value, " "))
		}
	}
	require.NotEmpty(t, data, "SSE frame has no data field: %q", frame)
	return strings.Join(data, "\n")
}
