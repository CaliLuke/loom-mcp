package progressivediscovery

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	mcpcatalog "example.com/assistant/progressive_discovery/gen/mcp_catalog"
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
