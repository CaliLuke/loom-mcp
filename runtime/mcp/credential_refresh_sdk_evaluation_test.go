package mcp

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCredentialRefreshSDKBoundary(t *testing.T) {
	for _, tc := range []struct {
		name           string
		absent, reject bool
	}{
		{name: "host renewal reaches SDK discovery and tool"},
		{name: "SDK does not replay a dispatched tool after 401", reject: true},
		{name: "absent host auth remains unauthorized", absent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tokens, requests, discoveries, initializations, tools, effects atomic.Int32
			server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "credential-evaluation", Version: "1"}, nil)
			server.AddTool(&sdkmcp.Tool{Name: "effect", InputSchema: jsontext.Value(`{"type":"object"}`)}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				effects.Add(1)
				return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "applied"}}}, nil
			})
			handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server {
				return server
			}, &sdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					tokens.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, err := io.WriteString(w, `{"access_token":"renewed-fixture","token_type":"Bearer","expires_in":3600}`)
					assert.NoError(t, err)
					return
				}
				requests.Add(1)
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.NoError(t, r.Body.Close())
				r.Body = io.NopCloser(bytes.NewReader(body))
				var message struct {
					Method string `json:"method"`
				}
				assert.NoError(t, json.Unmarshal(body, &message))
				switch message.Method {
				case "server/discover":
					discoveries.Add(1)
				case "initialize":
					initializations.Add(1)
				case "tools/call":
					tools.Add(1)
				}
				if r.Header.Get("Authorization") != "Bearer renewed-fixture" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if tc.reject && message.Method == "tools/call" {
					// The real SDK invokes the tool before this endpoint rejects the response.
					dispatched := httptest.NewRecorder()
					handler.ServeHTTP(dispatched, r)
					assert.Equal(t, http.StatusOK, dispatched.Code)
					assert.Equal(t, int32(1), effects.Load())
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				handler.ServeHTTP(w, r)
			}))
			t.Cleanup(endpoint.Close)
			host := endpoint.Client()
			if !tc.absent {
				host = credentialEvaluationClient(t.Context(), endpoint, true)
			}
			caller, err := NewHTTPCaller(t.Context(), HTTPOptions{Endpoint: endpoint.URL + "/mcp", Client: host})
			if tc.absent {
				require.Error(t, err)
				assert.Nil(t, caller)
				assert.Equal(t, int32(2), requests.Load())
				assert.Equal(t, int32(1), discoveries.Load())
				assert.Equal(t, int32(1), initializations.Load())
				assert.Zero(t, tokens.Load())
				assert.Zero(t, effects.Load())
				return
			}
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, caller.Close())
			})
			response, err := caller.CallTool(t.Context(), CallRequest{Tool: "effect", Payload: jsontext.Value(`{}`)})
			if tc.reject {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				var text string
				require.NoError(t, json.Unmarshal(response.Result, &text))
				assert.Equal(t, "applied", text)
			}
			assert.Equal(t, int32(1), tokens.Load())
			assert.Zero(t, initializations.Load())
			assert.Equal(t, int32(1), discoveries.Load())
			assert.Equal(t, int32(2), requests.Load())
			assert.Equal(t, int32(1), tools.Load())
			assert.Equal(t, int32(1), effects.Load())
		})
	}
}
