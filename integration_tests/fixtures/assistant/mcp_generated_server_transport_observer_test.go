package assistantapi

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
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
	require.Equal(t, loomtransport.EventKindRequestFinish, events[len(events)-1].Kind)
	require.Equal(t, loomtransport.TransportHTTP, events[len(events)-1].Transport)
}
