package assistantapi

import (
	"context"
	"testing"
	"time"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestGeneratedSDKServerReceivesClientRootsListChanged(t *testing.T) {
	notified := make(chan struct{}, 1)
	server, err := mcpassistant.NewSDKServer(NewAssistant(), withTestRuntimeCORS(t, &mcpassistant.SDKServerOptions{
		PromptProvider: promptProvider{},
		Server: &sdkmcp.ServerOptions{
			RootsListChangedHandler: func(context.Context, *sdkmcp.RootsListChangedRequest) {
				notified <- struct{}{}
			},
		},
	}))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := server.Server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, serverSession.Close()) }()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "roots-client", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, clientSession.Close()) }()

	client.AddRoots(&sdkmcp.Root{URI: "file:///workspace/new", Name: "New Root"})
	select {
	case <-notified:
	case <-ctx.Done():
		t.Fatal("notifications/roots/list_changed did not reach the generated SDK server")
	}
}
