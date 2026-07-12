package assistantapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	mcpAssistantjsonrpcc "example.com/assistant/gen/jsonrpc/mcp_assistant/client"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	mcpruntime "github.com/CaliLuke/loom-mcp/runtime/mcp"
	goahttp "github.com/CaliLuke/loom/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedJSONRPCServerToolsCallAcceptsOmittedOptionalArguments(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServer(t)
	defer server.Close()

	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	client := mcpAssistantjsonrpcc.NewClient(
		u.Scheme,
		u.Host,
		&http.Client{Timeout: 10 * time.Second},
		goahttp.RequestEncoder,
		goahttp.ResponseDecoder,
		false,
	)
	_, err = client.Initialize()(context.Background(), &mcpassistant.InitializePayload{
		ProtocolVersion: "2025-06-18",
		ClientInfo:      &mcpassistant.ClientInfo{Name: "omitted-arguments-proof", Version: "1.0.0"},
	})
	require.NoError(t, err)

	stream, err := client.ToolsCall()(context.Background(), &mcpassistant.ToolsCallPayload{Name: "search_records"})
	require.NoError(t, err)
	result, err := stream.(*mcpAssistantjsonrpcc.ToolsCallClientStream).Recv(context.Background())
	require.NoError(t, err)
	if result.IsError != nil {
		require.False(t, *result.IsError)
	}
	require.Len(t, result.Content, 1)
}

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
		assert.Contains(t, data, `"method":"mcp_assistant/stream.event"`)
		assert.Contains(t, data, message)
	case <-ctx.Done():
		t.Fatal("timed out waiting for events/stream notification")
	}
}

func TestGeneratedJSONRPCServerEventsStreamSendsPrimingEvent(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID, err := initializeJSONRPCSession(ctx, server.URL)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/rpc", nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
	req.Header.Set(mcpruntime.HeaderKeyProtocolVersion, mcpassistant.DefaultProtocolVersion)

	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	reader := bufio.NewReader(resp.Body)
	idLine, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Regexp(t, `^id: [0-9a-f]{32}\n$`, idLine)
	dataLine, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "data:\n", dataLine)
	separator, err := reader.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "\n", separator)
}

func TestGeneratedJSONRPCServerToolStreamSendsRetryBeforeClose(t *testing.T) {
	t.Parallel()

	server := newGeneratedJSONRPCServer(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID, err := initializeJSONRPCSession(ctx, server.URL)
	require.NoError(t, err)
	body := `{"jsonrpc":"2.0","id":"retry-proof","method":"tools/call","params":{"name":"multi_content","arguments":{"count":4}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/rpc", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(mcpruntime.HeaderKeySessionID, sessionID)
	req.Header.Set(mcpruntime.HeaderKeyProtocolVersion, mcpassistant.DefaultProtocolVersion)

	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	retryIndex := strings.LastIndex(string(data), "event: retry\nretry: 1000\ndata:\n\n")
	responseIndex := strings.LastIndex(string(data), `"id":"retry-proof"`)
	assert.GreaterOrEqual(t, retryIndex, 0, "terminal SSE response must carry a retry hint: %q", data)
	assert.Less(t, retryIndex, responseIndex, "retry hint must be sent before the terminal SSE response: %q", data)
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

	// The generated JSON-RPC client owns the MCP transport obligations
	// (Accept header, Mcp-Session-Id capture/replay, protocol version).
	client := mcpAssistantjsonrpcc.NewClient(
		u.Scheme,
		u.Host,
		&http.Client{Timeout: 10 * time.Second},
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
	require.NotNil(t, result.StructuredContent)
	structuredJSON, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	assert.Contains(t, string(structuredJSON), `"sentiment":"positive"`)

	var textContent map[string]any
	require.NoError(t, json.Unmarshal([]byte(*result.Content[0].Text), &textContent))
	var structuredContent map[string]any
	require.NoError(t, json.Unmarshal(structuredJSON, &structuredContent))
	assert.Equal(t, structuredContent, textContent)
}
