package assistantapi

import (
	"context"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestGeneratedSDKServerElicitsDuringToolCalls(t *testing.T) {
	generatedServer, sdkHTTPServer := newGeneratedSDKServer(t)
	defer sdkHTTPServer.Close()

	requests := make(chan *sdkmcp.ElicitRequest, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := generatedServer.Server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, serverSession.Close())
	}()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "fixture-sdk-client",
		Version: "1.0.0",
	}, &sdkmcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *sdkmcp.ElicitRequest) (*sdkmcp.ElicitResult, error) {
			requests <- req
			return &sdkmcp.ElicitResult{
				Action: "accept",
				Content: map[string]any{
					"summary": "The user supplied a better summary.",
				},
			}, nil
		},
	})
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, session.Close())
	}()

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "summarize_text",
		Arguments: map[string]any{
			"text": "needs-elicitation",
		},
	})
	require.NoError(t, err)

	require.Equal(t, map[string]any{
		"summary": "The user supplied a better summary.",
	}, result.StructuredContent)

	select {
	case req := <-requests:
		require.Equal(t, "form", req.Params.Mode)
		require.Equal(t, "Provide a summary for the requested text.", req.Params.Message)
		require.NotNil(t, req.Params.RequestedSchema)
	default:
		t.Fatal("expected generated SDK server to send elicitation/create to the client")
	}
}
