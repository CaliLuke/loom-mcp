package framework

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestGeneratedServerSupportsMultipleSDKStreamableHTTPSessions(t *testing.T) {
	t.Parallel()

	if !SupportsServer() {
		t.Skip("integration server not available; set TEST_SERVER_URL or restore the example directory")
	}

	r := NewRunner()
	r.skipGeneration = true
	require.NoError(t, r.startServer(t))
	t.Cleanup(r.stopServer)

	endpoint := r.baseURL.String() + "/rpc"

	first := connectSDKSession(t, endpoint, "itest-1")
	assertSDKToolCallWorks(t, first)
	require.NoError(t, first.Close())

	second := connectSDKSession(t, endpoint, "itest-2")
	assertSDKToolCallWorks(t, second)
	require.NoError(t, second.Close())
}

func connectSDKSession(t *testing.T, endpoint string, clientName string) *mcp.ClientSession {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	client := mcp.NewClient(&mcp.Implementation{
		Name:    clientName,
		Version: "1.0.0",
	}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	return session
}

func assertSDKToolCallWorks(t *testing.T, session *mcp.ClientSession) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "analyze_sentiment",
		Arguments: map[string]any{
			"text": "hello from sdk",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotEmpty(t, res.Content)
}
