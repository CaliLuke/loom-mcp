package assistantapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	assistant "example.com/assistant/gen/assistant"
	mcpAssistantadapter "example.com/assistant/gen/mcp_assistant/adapter/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goahttp "goa.design/goa/v3/http"
	"goa.design/goa/v3/jsonrpc"
)

func TestMCPClientAdapterMultiContentConcatenatesToolContent(t *testing.T) {
	t.Parallel()

	server := newMCPAdapterTestServer(t, map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": "{\"result\":\"hello ",
			},
			{
				"type": "text",
				"text": "world!\"}",
			},
		},
	})
	defer server.Close()

	out, err := newAdapterEndpoints(t, server).MultiContent(context.Background(), &assistant.MultiContentPayload{Count: 2})
	require.NoError(t, err)

	result, ok := out.(*assistant.MultiContentResult)
	require.True(t, ok)
	require.NotNil(t, result.Result)
	assert.Equal(t, "hello world!", *result.Result)
}

func TestMCPClientAdapterUsesLaterTextContentWhenFirstItemIsNonText(t *testing.T) {
	t.Parallel()

	server := newMCPAdapterTestServer(t, map[string]any{
		"content": []map[string]any{
			{
				"type":     "image",
				"data":     "ZmFrZS1pbWFnZQ==",
				"mimeType": "image/png",
			},
			{
				"type": "text",
				"text": "{\"result\":\"hello world from later text\"}",
			},
		},
	})
	defer server.Close()

	out, err := newAdapterEndpoints(t, server).MultiContent(context.Background(), &assistant.MultiContentPayload{Count: 3})
	require.NoError(t, err)

	result, ok := out.(*assistant.MultiContentResult)
	require.True(t, ok)
	require.NotNil(t, result.Result)
	assert.Equal(t, "hello world from later text", *result.Result)
}

func TestMCPClientAdapterReturnsRetryPromptOnInvalidToolResponse(t *testing.T) {
	t.Parallel()

	var initializeCalls int
	var toolCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/rpc", r.URL.Path)

		var req jsonrpc.Request
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

		switch req.Method {
		case "initialize":
			initializeCalls++
			writeJSONRPCBody(t, w, map[string]any{
				"jsonrpc": "2.0",
				"result": map[string]any{
					"protocolVersion": "2025-06-18",
					"capabilities":    map[string]any{},
					"serverInfo": map[string]any{
						"name":    "assistant-mcp",
						"version": "1.0.0",
					},
				},
			})
		case "tools/call":
			toolCalls++
			writeSSEJSONRPCBody(t, w, map[string]any{
				"jsonrpc": "2.0",
				"error": map[string]any{
					"code":    -32602,
					"message": "invalid execute_code payload",
				},
			})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()
	t.Cleanup(func() {
		assert.Equal(t, 1, initializeCalls)
		assert.Equal(t, 1, toolCalls)
	})

	_, err := newAdapterEndpoints(t, server).ExecuteCode(context.Background(), &assistant.ExecuteCodePayload{
		Language: "javascript",
		Code:     "console.log(1)",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Redo the operation now with valid parameters.")
	assert.Contains(t, err.Error(), `"language":{"type":"string"`)
}

func newMCPAdapterTestServer(t *testing.T, toolResult map[string]any) *httptest.Server {
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
					"capabilities": map[string]any{
						"tools":     map[string]any{},
						"resources": map[string]any{},
						"prompts":   map[string]any{},
					},
					"serverInfo": map[string]any{
						"name":    "assistant-mcp",
						"version": "1.0.0",
					},
				},
			}
		case "tools/call":
			toolCalls++
			require.GreaterOrEqual(t, initializeCalls, 1, "tools/call must follow initialize")
			response = map[string]any{
				"jsonrpc": "2.0",
				"result":  toolResult,
			}
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}

		if req.Method == "initialize" {
			writeJSONRPCBody(t, w, response)
			return
		}

		writeSSEJSONRPCBody(t, w, response)
	}))

	t.Cleanup(func() {
		assert.Equal(t, 1, initializeCalls)
		assert.Equal(t, 1, toolCalls)
	})

	return server
}

func newAdapterEndpoints(t *testing.T, server *httptest.Server) *assistant.Endpoints {
	t.Helper()

	u, err := url.Parse(server.URL)
	require.NoError(t, err)

	return mcpAssistantadapter.NewEndpoints(
		u.Scheme,
		u.Host,
		server.Client(),
		goahttp.RequestEncoder,
		goahttp.ResponseDecoder,
		false,
	)
}

func writeJSONRPCBody(t *testing.T, w http.ResponseWriter, response map[string]any) {
	t.Helper()

	data, err := json.Marshal(response)
	require.NoError(t, err)
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(data)
	require.NoError(t, err)
}

func writeSSEJSONRPCBody(t *testing.T, w http.ResponseWriter, response map[string]any) {
	t.Helper()

	data, err := json.Marshal(response)
	require.NoError(t, err)
	w.Header().Set("Content-Type", "text/event-stream")
	_, err = fmt.Fprintf(w, "event: response\n")
	require.NoError(t, err)
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	require.NoError(t, err)
}
