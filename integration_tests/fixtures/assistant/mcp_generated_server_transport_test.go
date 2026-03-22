package assistantapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	mcpruntime "github.com/CaliLuke/loom-mcp/runtime/mcp"
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
