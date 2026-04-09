package assistantapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	mcpAssistantjsonrpcc "example.com/assistant/gen/jsonrpc/mcp_assistant/client"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	mcpruntime "github.com/CaliLuke/loom-mcp/runtime/mcp"
	goahttp "github.com/CaliLuke/loom/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedJSONRPCServerEventsStreamPublishesNotifications(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID, err := initializeJSONRPCSession(ctx, server.URL)
	require.NoError(t, err)

	stream := openRawEventsStream(t, ctx, server, sessionID)
	defer stream.Close()

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
	case data := <-stream.Result():
		assert.NotContains(t, data, "ERROR:")
		assert.NotContains(t, data, "STATUS:")
		assert.Contains(t, data, `"method":"events/stream"`)
		assert.Contains(t, data, message)
	case <-ctx.Done():
		t.Fatal("timed out waiting for events/stream notification")
	}
}

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

func TestGeneratedJSONRPCServerToolsCallUsesCompactTextAndStructuredContent(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServer(t)
	defer server.Close()

	u, err := url.Parse(server.URL)
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
			Name:    "compact-text-proof",
			Version: "1.0.0",
		},
	})
	require.NoError(t, err)

	stream, err := client.ToolsCall()(context.Background(), &mcpassistant.ToolsCallPayload{
		Name:      "analyze_sentiment",
		Arguments: json.RawMessage(`{"text":"I love parity checks"}`),
	})
	require.NoError(t, err)

	clientStream := stream.(*mcpAssistantjsonrpcc.ToolsCallClientStream)
	result, err := clientStream.Recv(context.Background())
	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	require.NotNil(t, result.Content[0].Text)
	assert.Equal(t, "positive", *result.Content[0].Text)
	require.NotNil(t, result.StructuredContent)
	structuredJSON, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	assert.Contains(t, string(structuredJSON), `"sentiment":"positive"`)
}
