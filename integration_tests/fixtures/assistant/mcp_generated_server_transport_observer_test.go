package assistantapi

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
	loomtransport "github.com/CaliLuke/loom/observability/transport"
	"github.com/stretchr/testify/require"
)

type recordingTransportObserver struct {
	mu     sync.Mutex
	events []loomtransport.Event
}

func (o *recordingTransportObserver) ObserveEvent(_ context.Context, event loomtransport.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *recordingTransportObserver) snapshot() []loomtransport.Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]loomtransport.Event(nil), o.events...)
}

func TestGeneratedSDKServerTransportObserverOption(t *testing.T) {
	observer := new(recordingTransportObserver)
	sdkServer, err := mcpassistant.NewSDKServer(NewAssistant(), withTestRuntimeCORS(t, &mcpassistant.SDKServerOptions{
		PromptProvider:    promptProvider{},
		TransportObserver: observer,
	}))
	require.NoError(t, err)
	server := httptest.NewServer(sdkServer.Handler)
	defer server.Close()

	response := postSDKInitializeWithOrigin(t, server.URL, server.URL)
	require.Less(t, response.StatusCode, 400)

	events := observer.snapshot()
	require.NotEmpty(t, events)
	require.Equal(t, loomtransport.EventKindRequestStart, events[0].Kind)
	require.Equal(t, loomtransport.EventKindRequestFinish, events[len(events)-1].Kind)
	require.Equal(t, loomtransport.TransportHTTP, events[len(events)-1].Transport)
}
