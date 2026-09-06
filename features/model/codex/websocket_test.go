package codex

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestWebSocketUsesFixedIdentityAndResponseCreateFrame(t *testing.T) {
	var connections atomic.Int32
	client := newWebSocketTestClient(t, func(request *http.Request, conn *websocket.Conn) {
		connections.Add(1)
		assert.Equal(t, "/backend-api/codex/responses", request.URL.Path)
		assert.Equal(t, "chatgpt.com", request.Host)
		assert.Equal(t, "Bearer secret-token", request.Header.Get("Authorization"))
		assert.Equal(t, "account-1", request.Header.Get("chatgpt-account-id"))
		assert.Equal(t, "us", request.Header.Get("x-openai-internal-codex-residency"))
		assert.Equal(t, webSocketBetaHeader, request.Header.Get("OpenAI-Beta"))
		assert.Equal(t, "pi", request.Header.Get("originator"))
		assert.Equal(t, defaultClientVersion, request.Header.Get("version"))
		assert.Equal(t, codexUserAgent, request.Header.Get("User-Agent"))
		assert.Empty(t, request.Header.Get("Content-Type"))
		assert.Empty(t, request.Header.Get("Sec-Websocket-Protocol"))
		for _, header := range []string{
			"x-api-key", "session_id", "installation", "attestation", "x-models-etag",
			"conversation_id", "x-openai-internal-codex-conversation-id",
			"x-openai-internal-codex-remote-compaction", "x-codex-credit", "content-encoding",
		} {
			assert.Empty(t, request.Header.Get(header), header)
		}
		var frame map[string]any
		require.NoError(t, conn.ReadJSON(&frame))
		assert.Equal(t, "response.create", frame["type"])
		_, chained := frame["previous_response_id"]
		assert.False(t, chained)
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(textTerminalEvent("websocket"))))
		require.NoError(t, conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second)))
	}, nil)

	response, err := client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
	require.Len(t, response.Content, 1)
	assert.Equal(t, int32(1), connections.Load())
}

func TestWebSocketResponsesLiteWireShape(t *testing.T) {
	client := newWebSocketTestClientWithOptions(t, defaultIdleTimeout, func(request *http.Request, conn *websocket.Conn) {
		assert.Equal(t, "true", request.Header.Get(responsesLiteHeader))
		var frame map[string]any
		require.NoError(t, conn.ReadJSON(&frame))
		assert.Equal(t, map[string]any{responsesLiteMetadata: "true"}, frame["client_metadata"])
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(emptyTerminalEvent())))
	}, nil, true)
	_, err := client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
}

func TestAutoTransportFallsBackBeforeOutputAndResolvesCredentialsOnce(t *testing.T) {
	credentialCalls := 0
	httpCalls := 0
	client, err := New(Options{
		CredentialSource: CredentialSourceFunc(func(context.Context) (Credentials, error) {
			credentialCalls++
			return Credentials{AccessToken: "token", AccountID: "account"}, nil
		}),
		WebSocketDialer: &websocket.Dialer{NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("upgrade network failure")
		}},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			httpCalls++
			return sseResponse(http.StatusOK, sseEvents(emptyTerminalEvent())), nil
		})},
		Transport: TransportAuto, DefaultModel: "model",
	})
	require.NoError(t, err)
	_, err = client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
	assert.Equal(t, 1, credentialCalls)
	assert.Equal(t, 1, httpCalls)
}

func TestAutoTransportDoesNotFallbackAfterOutput(t *testing.T) {
	var httpCalls atomic.Int32
	client := newWebSocketTestClient(t, func(_ *http.Request, conn *websocket.Conn) {
		var frame map[string]any
		require.NoError(t, conn.ReadJSON(&frame))
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg","type":"message"}}`)))
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.output_text.delta","item_id":"msg","output_index":0,"content_index":0,"delta":"partial"}`)))
		require.NoError(t, conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second)))
	}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls.Add(1)
		return sseResponse(http.StatusOK, sseEvents(emptyTerminalEvent())), nil
	}))

	stream, err := client.Stream(context.Background(), testRequest())
	require.NoError(t, err)
	_, err = stream.Recv()
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Error(t, err)
	assert.Equal(t, int32(0), httpCalls.Load())
}

func TestAutoTransportDoesNotFallbackOnMalformedOrAuthFailure(t *testing.T) {
	t.Run("malformed frame", func(t *testing.T) {
		var httpCalls atomic.Int32
		client := newWebSocketTestClient(t, func(_ *http.Request, conn *websocket.Conn) {
			var frame map[string]any
			require.NoError(t, conn.ReadJSON(&frame))
			require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{`)))
		}, roundTripFunc(func(*http.Request) (*http.Response, error) {
			httpCalls.Add(1)
			return sseResponse(http.StatusOK, sseEvents(emptyTerminalEvent())), nil
		}))
		_, err := client.Complete(context.Background(), testRequest())
		requireInvalidStreamError(t, err)
		assert.Zero(t, httpCalls.Load())
	})

	t.Run("authentication failure", func(t *testing.T) {
		var httpCalls atomic.Int32
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer server.Close()
		client, err := New(Options{
			CredentialSource: CredentialSourceFunc(testCredentials),
			WebSocketDialer:  testWebSocketDialer(server),
			HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				httpCalls.Add(1)
				return sseResponse(http.StatusOK, sseEvents(emptyTerminalEvent())), nil
			})},
			Transport: TransportAuto, DefaultModel: "model",
		})
		require.NoError(t, err)
		stream, err := client.Stream(context.Background(), testRequest())
		require.Nil(t, stream)
		providerErr, ok := asProviderError(err)
		require.True(t, ok)
		assert.Equal(t, http.StatusUnauthorized, providerErr.HTTPStatus())
		assert.Zero(t, httpCalls.Load())
	})
}

func TestWebSocketUsesIndependentConcurrentConnections(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	client := newWebSocketTestClient(t, func(_ *http.Request, conn *websocket.Conn) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		var frame map[string]any
		require.NoError(t, conn.ReadJSON(&frame))
		time.Sleep(20 * time.Millisecond)
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(emptyTerminalEvent())))
	}, nil)

	var group sync.WaitGroup
	errorsCh := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := client.Complete(context.Background(), testRequest())
			errorsCh <- err
		}()
	}
	group.Wait()
	close(errorsCh)
	for err := range errorsCh {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(2), maximum.Load())
}

func TestWebSocketIdleTimeoutFailsIncompleteStream(t *testing.T) {
	client := newWebSocketTestClientWithTimeout(t, 20*time.Millisecond, func(_ *http.Request, conn *websocket.Conn) {
		var frame map[string]any
		require.NoError(t, conn.ReadJSON(&frame))
		time.Sleep(100 * time.Millisecond)
	})
	stream, err := client.Stream(context.Background(), testRequest())
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Error(t, err)
	assert.Nil(t, stream.Response())
}

func TestWebSocketIdleTimeoutResetsAfterAcceptedEvents(t *testing.T) {
	const (
		idle  = time.Second
		pause = 600 * time.Millisecond
	)
	client := newWebSocketTestClientWithTimeout(t, idle, func(_ *http.Request, conn *websocket.Conn) {
		var frame map[string]any
		require.NoError(t, conn.ReadJSON(&frame))
		for _, event := range []string{`{"type":"response.created","response":{"id":"resp-1","status":"in_progress"}}`, emptyTerminalEvent()} {
			time.Sleep(pause)
			require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(event)))
		}
	})
	_, err := client.Complete(context.Background(), testRequest())
	require.NoError(t, err)
}

func newWebSocketTestClient(t *testing.T, handler func(*http.Request, *websocket.Conn), fallback http.RoundTripper) *Client {
	t.Helper()
	return newWebSocketTestClientWithOptions(t, defaultIdleTimeout, handler, fallback, false)
}

func newWebSocketTestClientWithTimeout(t *testing.T, timeout time.Duration, handler func(*http.Request, *websocket.Conn)) *Client {
	t.Helper()
	return newWebSocketTestClientWithOptions(t, timeout, handler, nil, false)
}

func newWebSocketTestClientWithOptions(t *testing.T, timeout time.Duration, handler func(*http.Request, *websocket.Conn), fallback http.RoundTripper, lite bool) *Client {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(w, request, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		handler(request, conn)
	}))
	t.Cleanup(server.Close)
	options := Options{
		CredentialSource:  CredentialSourceFunc(testCredentials),
		WebSocketDialer:   testWebSocketDialer(server),
		Transport:         TransportWebSocket,
		StreamIdleTimeout: timeout,
		DefaultModel:      "gpt-codex",
		ResponsesLite:     lite,
	}
	if fallback != nil {
		options.Transport = TransportAuto
		options.HTTPClient = &http.Client{Transport: fallback}
	}
	client, err := New(options)
	require.NoError(t, err)
	return client
}

func testWebSocketDialer(server *httptest.Server) *websocket.Dialer {
	return &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // The dialer targets a test-only TLS server.
	}
}

func asProviderError(err error) (*model.ProviderError, bool) {
	return model.AsProviderError(err)
}

func TestAutoTransportDoesNotFallbackOnNormalCloseBeforeOutput(t *testing.T) {
	var httpCalls atomic.Int32
	client := newWebSocketTestClient(t, func(_ *http.Request, conn *websocket.Conn) {
		var frame map[string]any
		require.NoError(t, conn.ReadJSON(&frame))
		require.NoError(t, conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "complete without terminal"),
			time.Now().Add(time.Second),
		))
	}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls.Add(1)
		return sseResponse(http.StatusOK, sseEvents(emptyTerminalEvent())), nil
	}))

	_, err := client.Complete(context.Background(), testRequest())
	requireInvalidStreamError(t, err)
	assert.Zero(t, httpCalls.Load())
}

func TestWebSocketFrameLimit(t *testing.T) {
	terminal := emptyTerminalEvent()
	tests := []struct {
		name    string
		size    int
		wantErr bool
	}{
		{name: "exact limit", size: maxStreamEventBytes},
		{name: "over limit", size: maxStreamEventBytes + 1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := terminal + strings.Repeat(" ", tt.size-len(terminal))
			client := newWebSocketTestClient(t, func(_ *http.Request, conn *websocket.Conn) {
				var frame map[string]any
				require.NoError(t, conn.ReadJSON(&frame))
				_ = conn.WriteMessage(websocket.TextMessage, []byte(payload))
			}, nil)
			response, err := client.Complete(context.Background(), testRequest())
			if tt.wantErr {
				require.Nil(t, response)
				requireInvalidStreamError(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "completed", response.StopReason)
		})
	}
}

func TestWebSocketUpgradeErrorBodyLimitPreservesSafeMetadata(t *testing.T) {
	base := `{"error":{"code":"invalid_request"}}`
	for _, size := range []int{maxHTTPErrorBodyBytes, maxHTTPErrorBodyBytes + 1} {
		t.Run(fmt.Sprintf("size_%d", size), func(t *testing.T) {
			body := base + strings.Repeat(" ", size-len(base))
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("x-request-id", "request-safe")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			client, err := New(Options{
				CredentialSource: CredentialSourceFunc(testCredentials),
				WebSocketDialer:  testWebSocketDialer(server),
				Transport:        TransportWebSocket,
				DefaultModel:     "model",
			})
			require.NoError(t, err)
			_, err = client.Complete(context.Background(), testRequest())
			providerErr, ok := model.AsProviderError(err)
			require.True(t, ok)
			assert.Equal(t, "invalid_request", providerErr.Code())
			assert.Equal(t, "request-safe", providerErr.RequestID())
			assert.Equal(t, http.StatusBadRequest, providerErr.HTTPStatus())
		})
	}
}
