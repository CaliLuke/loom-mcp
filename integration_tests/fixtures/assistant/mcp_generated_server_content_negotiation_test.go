package assistantapi

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpassistant "example.com/assistant/gen/mcp_assistant"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedSDKServerNegotiatesToolCallResponse(t *testing.T) {
	t.Run("official SDK client", func(t *testing.T) {
		session := connectSDKSessionToServer(t, newGeneratedSDKServerURL(t), nil)
		defer func() {
			require.NoError(t, session.Close())
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "projected_status_tool"})
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"status": "ready"}, result.StructuredContent)
	})

	t.Run("JSON-only raw client", func(t *testing.T) {
		_, server := newGeneratedStatelessSDKServer(t)
		defer server.Close()

		body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"projected_status_tool","arguments":{}}}`)
		req, err := http.NewRequest(http.MethodPost, server.URL+"/rpc", bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := server.Client().Do(req)
		require.NoError(t, err)
		defer func() {
			require.NoError(t, resp.Body.Close())
		}()
		responseBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, http.StatusNotAcceptable, resp.StatusCode, "tools/call must reject an unsupported response representation instead of sending SSE: %s", responseBody)
		assert.NotEqual(t, "text/event-stream", resp.Header.Get("Content-Type"))
	})

	t.Run("configured JSON response", func(t *testing.T) {
		sdkServer, err := mcpassistant.NewSDKServer(NewAssistant(), &mcpassistant.SDKServerOptions{
			PromptProvider: promptProvider{},
			StreamableHTTP: &sdkmcp.StreamableHTTPOptions{
				Stateless:    true,
				JSONResponse: true,
			},
		})
		require.NoError(t, err)
		mux := http.NewServeMux()
		mux.Handle("/rpc", sdkServer.Handler)
		server := httptest.NewServer(mux)
		defer server.Close()

		body := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"projected_status_tool","arguments":{}}}`)
		req, err := http.NewRequest(http.MethodPost, server.URL+"/rpc", bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := server.Client().Do(req)
		require.NoError(t, err)
		defer func() {
			require.NoError(t, resp.Body.Close())
		}()
		responseBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		require.Equal(t, http.StatusOK, resp.StatusCode, "tools/call must use plain JSON when the configured representation is accepted: %s", responseBody)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		var rpcResponse struct {
			JSONRPC string `json:"jsonrpc"`
			ID      int    `json:"id"`
			Result  struct {
				StructuredContent map[string]any `json:"structuredContent"`
			} `json:"result"`
		}
		require.NoError(t, json.Unmarshal(responseBody, &rpcResponse))
		assert.Equal(t, "2.0", rpcResponse.JSONRPC)
		assert.Equal(t, 2, rpcResponse.ID)
		assert.Equal(t, map[string]any{"status": "ready"}, rpcResponse.Result.StructuredContent)
	})
}
