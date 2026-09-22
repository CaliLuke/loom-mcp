package assistantapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	loomtransport "github.com/CaliLuke/loom/observability/transport"
	"github.com/stretchr/testify/require"
)

type recordingTransportObserver struct {
	mu           sync.Mutex
	terminalOnce sync.Once
	events       []loomtransport.Event
	terminal     chan struct{}
}

func (o *recordingTransportObserver) ObserveEvent(_ context.Context, event loomtransport.Event) {
	o.mu.Lock()
	o.events = append(o.events, event)
	o.mu.Unlock()

	if event.Kind == loomtransport.EventKindRequestFinish || event.Kind == loomtransport.EventKindRequestFailure {
		o.terminalOnce.Do(func() {
			close(o.terminal)
		})
	}
}

func (o *recordingTransportObserver) snapshot() []loomtransport.Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]loomtransport.Event(nil), o.events...)
}

func (o *recordingTransportObserver) waitForTerminal(t *testing.T) {
	t.Helper()
	select {
	case <-o.terminal:
	case <-time.After(time.Second):
		require.FailNow(t, "timed out waiting for terminal transport observer event")
	}
}

func (o *recordingTransportObserver) waitForTerminalCount(t *testing.T, count int) {
	t.Helper()
	require.Eventually(t, func() bool {
		o.mu.Lock()
		defer o.mu.Unlock()
		terminals := 0
		for _, event := range o.events {
			if event.Kind == loomtransport.EventKindRequestFinish || event.Kind == loomtransport.EventKindRequestFailure {
				terminals++
			}
		}
		return terminals >= count
	}, time.Second, time.Millisecond)
}

func TestGeneratedSDKServerTransportObserverOption(t *testing.T) {
	observer := &recordingTransportObserver{terminal: make(chan struct{})}
	sdkServer, err := mcpassistant.NewSDKServer(NewAssistant(), withTestRuntimeCORS(t, &mcpassistant.SDKServerOptions{
		PromptProvider:    promptProvider{},
		TransportObserver: observer,
	}))
	require.NoError(t, err)
	server := httptest.NewServer(sdkServer.Handler)
	defer server.Close()

	response := postSDKInitializeWithOrigin(t, server.URL, server.URL)
	require.Less(t, response.StatusCode, 400)
	observer.waitForTerminal(t)

	events := observer.snapshot()
	require.NotEmpty(t, events)
	require.Equal(t, loomtransport.EventKindRequestStart, events[0].Kind)
	terminal := events[len(events)-1]
	require.Equal(t, loomtransport.EventKindRequestFinish, terminal.Kind)
	require.Equal(t, loomtransport.TransportHTTP, terminal.Transport)
	require.Equal(t, "initialize", terminal.JSONRPCMethod)
	require.Equal(t, "1", terminal.JSONRPCID)
	require.False(t, terminal.Notification)
	sessionID := response.Header.Get(mcpruntime.HeaderKeySessionID)
	require.NotEmpty(t, sessionID)
	require.Equal(t, sessionID, terminal.SessionID)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, bytes.NewBufferString(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	request.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
	listResponse, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, listResponse.Body.Close())
	})
	require.Less(t, listResponse.StatusCode, 400)
	observer.waitForTerminalCount(t, 2)

	events = observer.snapshot()
	terminal = events[len(events)-1]
	require.Equal(t, loomtransport.EventKindRequestFinish, terminal.Kind)
	require.Equal(t, "tools/list", terminal.JSONRPCMethod)
	require.Equal(t, "2", terminal.JSONRPCID)
	require.Equal(t, sessionID, terminal.SessionID)

	notificationRequest, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, bytes.NewBufferString(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	require.NoError(t, err)
	notificationRequest.Header = request.Header.Clone()
	notificationResponse, err := http.DefaultClient.Do(notificationRequest)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, notificationResponse.Body.Close())
	})
	require.Less(t, notificationResponse.StatusCode, 400)
	observer.waitForTerminalCount(t, 3)
	events = observer.snapshot()
	terminal = events[len(events)-1]
	require.Equal(t, "notifications/initialized", terminal.JSONRPCMethod)
	require.Empty(t, terminal.JSONRPCID)
	require.True(t, terminal.Notification)
	require.Equal(t, sessionID, terminal.SessionID)

	batchRequest, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, bytes.NewBufferString(`[{"jsonrpc":"2.0","id":3,"method":"ping"}]`))
	require.NoError(t, err)
	batchRequest.Header.Set("Content-Type", "application/json")
	batchRequest.Header.Set("Accept", "application/json, text/event-stream")
	batchRequest.Header.Set("MCP-Protocol-Version", "2025-11-25")
	batchRequest.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
	batchResponse, err := http.DefaultClient.Do(batchRequest)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, batchResponse.Body.Close())
	})
	observer.waitForTerminalCount(t, 4)

	events = observer.snapshot()
	terminal = events[len(events)-1]
	require.Empty(t, terminal.JSONRPCMethod)
	require.Empty(t, terminal.JSONRPCID)
	require.Zero(t, terminal.BatchCount)
	require.Equal(t, sessionID, terminal.SessionID)
}

func TestGeneratedSDKServerObservesRejectedOrigin(t *testing.T) {
	observer := &recordingTransportObserver{terminal: make(chan struct{})}
	sdkServer, err := mcpassistant.NewSDKServer(NewAssistant(), withTestRuntimeCORS(t, &mcpassistant.SDKServerOptions{
		PromptProvider:    promptProvider{},
		TransportObserver: observer,
	}))
	require.NoError(t, err)
	server := httptest.NewServer(sdkServer.Handler)
	defer server.Close()

	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		server.URL,
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"observer-test","version":"1.0.0"}}}`)),
	)
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set(mcpruntime.HeaderKeySessionID, "attacker-session")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, response.Body.Close())
	})
	require.Equal(t, 403, response.StatusCode)
	observer.waitForTerminal(t)

	events := observer.snapshot()
	require.NotEmpty(t, events)
	require.Equal(t, loomtransport.EventKindRequestStart, events[0].Kind)
	terminal := events[len(events)-1]
	require.Equal(t, loomtransport.EventKindRequestFailure, terminal.Kind)
	require.Empty(t, terminal.JSONRPCMethod)
	require.Empty(t, terminal.JSONRPCID)
	require.Empty(t, terminal.SessionID)
}

func TestGeneratedSDKServerObservesMalformedRequestWithoutProtocolFields(t *testing.T) {
	observer := &recordingTransportObserver{terminal: make(chan struct{})}
	sdkServer, err := mcpassistant.NewSDKServer(NewAssistant(), withTestRuntimeCORS(t, &mcpassistant.SDKServerOptions{
		PromptProvider:    promptProvider{},
		TransportObserver: observer,
	}))
	require.NoError(t, err)
	server := httptest.NewServer(sdkServer.Handler)
	defer server.Close()

	initializeResponse := postSDKInitializeWithOrigin(t, server.URL, server.URL)
	require.Less(t, initializeResponse.StatusCode, http.StatusBadRequest)
	sessionID := initializeResponse.Header.Get(mcpruntime.HeaderKeySessionID)
	require.NotEmpty(t, sessionID)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, bytes.NewBufferString(`{"jsonrpc":`))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-11-25")
	request.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, response.Body.Close())
	})
	require.GreaterOrEqual(t, response.StatusCode, http.StatusBadRequest)
	observer.waitForTerminal(t)

	events := observer.snapshot()
	require.NotEmpty(t, events)
	terminal := events[len(events)-1]
	require.Equal(t, loomtransport.EventKindRequestFailure, terminal.Kind)
	require.Empty(t, terminal.JSONRPCMethod)
	require.Empty(t, terminal.JSONRPCID)
	require.False(t, terminal.Notification)
	require.Equal(t, sessionID, terminal.SessionID)
}
