package assistantapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	mcpAssistantjsonrpcc "example.com/assistant/gen/jsonrpc/mcp_assistant/client"
	mcpassistant "example.com/assistant/gen/mcp_assistant"
	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	goahttp "github.com/CaliLuke/loom/http"
	"github.com/CaliLuke/loom/jsonrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedNewCallerConcatenatesTextContent(t *testing.T) {
	t.Parallel()

	server := newGeneratedCallerTestServer(t, map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": "{\"result\":\"hello ",
			},
			{
				"type": "text",
				"text": "world\"}",
			},
		},
	})
	defer server.Close()

	caller := newGeneratedCaller(t, server)
	resp, err := caller.CallTool(context.Background(), mcpruntime.CallRequest{
		Tool:    "multi_content",
		Payload: json.RawMessage(`{"count":2}`),
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"result":"hello world"}`, string(resp.Result))

	var structured []map[string]any
	require.NoError(t, json.Unmarshal(resp.Structured, &structured))
	require.Len(t, structured, 2)
	assert.Equal(t, "text", structured[0]["type"])
	assert.Equal(t, "text", structured[1]["type"])
}

func TestGeneratedNewCallerFallsBackToStructuredContentWhenNoTextExists(t *testing.T) {
	t.Parallel()

	server := newGeneratedCallerTestServer(t, map[string]any{
		"content": []map[string]any{
			{
				"type":     "image",
				"data":     "ZmFrZS1pbWFnZQ==",
				"mimeType": "image/png",
			},
		},
	})
	defer server.Close()

	caller := newGeneratedCaller(t, server)
	resp, err := caller.CallTool(context.Background(), mcpruntime.CallRequest{
		Tool:    "multi_content",
		Payload: json.RawMessage(`{"count":4}`),
	})
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.Equal(t, "image", result["type"])
	assert.Equal(t, "image/png", result["mimeType"])
	assert.Equal(t, "ZmFrZS1pbWFnZQ==", result["data"])
}

func TestGeneratedNewCallerAcceptsStructuredContentWithEmptyContent(t *testing.T) {
	t.Parallel()

	server := newGeneratedCallerTestServer(t, map[string]any{
		"content":           []map[string]any{},
		"structuredContent": map[string]any{"count": 3, "status": "ok"},
	})
	defer server.Close()

	caller := newGeneratedCaller(t, server)
	resp, err := caller.CallTool(context.Background(), mcpruntime.CallRequest{
		Tool:    "multi_content",
		Payload: json.RawMessage(`{"count":0}`),
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"count":3,"status":"ok"}`, string(resp.Result))
	assert.Empty(t, resp.Structured)
}

func TestGeneratedNewCallerRejectsEmptyStructuredContentWithEmptyContent(t *testing.T) {
	t.Parallel()

	for name, structured := range map[string]any{
		"map":   map[string]any{},
		"slice": []any{},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			server := newGeneratedCallerTestServer(t, map[string]any{
				"content":           []map[string]any{},
				"structuredContent": structured,
			})
			defer server.Close()

			caller := newGeneratedCaller(t, server)
			_, err := caller.CallTool(context.Background(), mcpruntime.CallRequest{
				Tool:    "multi_content",
				Payload: json.RawMessage(`{"count":0}`),
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "empty MCP response")
		})
	}
}

func TestGeneratedNewCallerEmptyResponseIncludesToolContext(t *testing.T) {
	t.Parallel()

	server := newGeneratedCallerTestServer(t, map[string]any{
		"content": []map[string]any{},
	})
	defer server.Close()

	caller := newGeneratedCaller(t, server)
	_, err := caller.CallTool(context.Background(), mcpruntime.CallRequest{
		Tool:    "multi_content",
		Payload: json.RawMessage(`{"count":0}`),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `suite "assistant-mcp"`)
	assert.Contains(t, err.Error(), `tool "multi_content"`)
}

func TestGeneratedNewCallerClosesStreamAfterDecodeFailure(t *testing.T) {
	t.Parallel()

	bodyReleased := make(chan struct{})
	releaseHandler := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonrpc.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Method == "initialize" {
			w.Header().Set("Content-Type", "application/json")
			if _, err := fmt.Fprint(w, `{"jsonrpc":"2.0","result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"assistant-mcp","version":"1.0.0"}}}`); err != nil {
				return
			}
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := fmt.Fprint(w, "event: response\ndata: {\"jsonrpc\":\"2.0\",\"result\":{\"content\":\"invalid\"}}\n\n"); err != nil {
			return
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		select {
		case <-r.Context().Done():
			close(bodyReleased)
		case <-releaseHandler:
		}
	}))
	defer server.Close()
	defer close(releaseHandler)

	caller := newGeneratedCaller(t, server)
	_, err := caller.CallTool(context.Background(), mcpruntime.CallRequest{
		Tool:    "multi_content",
		Payload: json.RawMessage(`{"count":1}`),
	})
	require.Error(t, err)

	select {
	case <-bodyReleased:
	case <-time.After(time.Second):
		t.Fatal("generated caller did not close the tools/call response body after a decode failure")
	}
}

func newGeneratedCaller(t *testing.T, server *httptest.Server) mcpruntime.Caller {
	t.Helper()

	u, err := url.Parse(server.URL)
	require.NoError(t, err)

	client := mcpAssistantjsonrpcc.NewClient(
		u.Scheme,
		u.Host,
		server.Client(),
		goahttp.RequestEncoder,
		goahttp.ResponseDecoder,
		false,
	)
	_, err = client.Initialize()(context.Background(), &mcpassistant.InitializePayload{
		ProtocolVersion: "2025-06-18",
		ClientInfo: &mcpassistant.ClientInfo{
			Name:    "generated-caller-test",
			Version: "1.0.0",
		},
	})
	require.NoError(t, err)

	return mcpAssistantjsonrpcc.NewCaller(client, "assistant-mcp")
}

func newGeneratedCallerTestServer(t *testing.T, toolResult map[string]any) *httptest.Server {
	t.Helper()

	var initializeCalls int
	var toolCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/rpc", r.URL.Path)

		var req jsonrpc.Request
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

		var response map[string]any
		switch req.Method {
		case "initialize":
			initializeCalls++
			response = map[string]any{
				"jsonrpc": "2.0",
				"result": map[string]any{
					"protocolVersion": "2025-06-18",
					"capabilities":    map[string]any{},
					"serverInfo": map[string]any{
						"name":    "assistant-mcp",
						"version": "1.0.0",
					},
				},
			}
		case "tools/call":
			toolCalls++
			response = map[string]any{
				"jsonrpc": "2.0",
				"result":  toolResult,
			}
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}

		data, err := json.Marshal(response)
		require.NoError(t, err)

		if req.Method == "initialize" {
			w.Header().Set("Content-Type", "application/json")
			_, err = w.Write(data)
			require.NoError(t, err)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, err = fmt.Fprintf(w, "event: response\n")
		require.NoError(t, err)
		_, err = fmt.Fprintf(w, "data: %s\n\n", data)
		require.NoError(t, err)
	}))

	t.Cleanup(func() {
		assert.Equal(t, 1, initializeCalls)
		assert.Equal(t, 1, toolCalls)
	})

	return server
}
