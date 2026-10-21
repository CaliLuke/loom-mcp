package codex

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPErrorRetainsTransportPhase(t *testing.T) {
	for _, tc := range []struct {
		name      string
		transport Transport
		want      string
		wsCalls   int32
		httpCalls int32
	}{
		{"sse", TransportSSE, "responses.sse.request", 0, 1},
		{"websocket", TransportWebSocket, "responses.websocket.upgrade", 1, 0},
		{"auto", TransportAuto, "responses.websocket.upgrade", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var wsCalls, httpCalls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				wsCalls.Add(1)
				w.Header().Set("x-request-id", "request-secret-token")
				w.WriteHeader(http.StatusForbidden)
				_, err := w.Write([]byte(`{"error":{"code":"denied-secret-token","message":"private body"}}`))
				assert.NoError(t, err)
			}))
			t.Cleanup(server.Close)
			client, err := New(Options{
				CredentialSource: CredentialSourceFunc(testCredentials),
				WebSocketDialer:  testWebSocketDialer(server),
				HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					httpCalls.Add(1)
					return sseResponse(http.StatusForbidden, `{"error":{"code":"denied-secret-token","message":"private body"}}`), nil
				})},
				Transport: tc.transport, DefaultModel: "model",
			})
			require.NoError(t, err)
			_, err = client.Complete(t.Context(), testRequest())
			pe, ok := model.AsProviderError(err)
			require.True(t, ok)
			assert.Equal(t, tc.want, pe.Operation())
			assert.Equal(t, http.StatusForbidden, pe.HTTPStatus())
			assert.Equal(t, model.ProviderErrorKindAuth, pe.Kind())
			assert.False(t, pe.Retryable())
			assert.Empty(t, pe.Code())
			assert.Empty(t, pe.RequestID())
			assert.NotContains(t, err.Error(), "secret-token")
			assert.NotContains(t, err.Error(), "private body")
			assert.Equal(t, tc.wsCalls, wsCalls.Load())
			assert.Equal(t, tc.httpCalls, httpCalls.Load())
		})
	}
}
