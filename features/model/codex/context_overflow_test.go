package codex

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// Adapted from the recorded context_window_error_is_fatal fixture at:
// https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/sse/responses.rs
const codexOverflowBody = `{"error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"Your input exceeds the context window of this model. Please adjust your input and try again."}}`

func testContextOverflow(t *testing.T) {
	client := codexOverflowHTTPClient(t, codexOverflowBody)
	response, err := client.Complete(context.Background(), testRequest())
	assert.Nil(t, response)
	pe := requireCodexOverflow(t, err)
	assert.Equal(t, 400, pe.HTTPStatus())
	assert.Equal(t, "req_overflow", pe.RequestID())

	for _, body := range []string{
		`{"error":{"code":"invalid_request","message":"context_length_exceeded: secret-token account-1"}}`,
		`{"error":{"type":"context_length_exceeded","message":"Your input exceeds the context window of this model."}}`,
	} {
		t.Run(body, func(t *testing.T) {
			response, err := codexOverflowHTTPClient(t, body).Complete(context.Background(), testRequest())
			assert.Nil(t, response)
			var ordinary *model.ProviderError
			require.ErrorAs(t, err, &ordinary)
			assert.Equal(t, model.ProviderErrorKindInvalidRequest, ordinary.Kind())
			assert.False(t, ordinary.Retryable())
			assert.Equal(t, "provider rejected the request", ordinary.Message())
		})
	}
}

func testStreamContextOverflow(t *testing.T) {
	t.Run("HTTP rejection", func(t *testing.T) {
		stream, err := codexOverflowHTTPClient(t, codexOverflowBody).Stream(context.Background(), testRequest())
		assert.Nil(t, stream)
		assert.Equal(t, "responses.sse.request", requireCodexOverflow(t, err).Operation())
	})
	for _, body := range []string{
		`{"type":"response.failed","sequence_number":3,"response":{"id":"resp_1","object":"response","status":"failed","error":{"code":"context_length_exceeded","message":"Your input exceeds the context window of this model. Please adjust your input and try again."},"usage":null}}`,
		`{"type":"error","code":"context_length_exceeded","message":"secret-token account-1","request_id":"req_overflow"}`,
		`{"type":"error","error":{"code":"context_length_exceeded","status":400,"request_id":"req_overflow","message":"secret-token account-1"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			client := newSSETestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return sseResponse(http.StatusOK, sseEvents(body)), nil
			}))
			stream, err := client.Stream(context.Background(), testRequest())
			require.NoError(t, err)
			defer func() { assert.NoError(t, stream.Close()) }()
			chunk, err := stream.Recv()
			assert.Nil(t, chunk)
			assert.Equal(t, "responses", requireCodexOverflow(t, err).Operation())
			assert.Nil(t, stream.Response())
		})
	}
}

// Error types and messages do not identify context overflow without its code.
func TestStreamContextOverflowRequiresCode(t *testing.T) {
	for _, body := range []string{
		`{"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"type":"context_length_exceeded","status":400,"message":"context_length_exceeded"}}}`,
		`{"type":"error","error":{"code":"invalid_request","status":400,"message":"context_length_exceeded: secret-token account-1"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			client := newSSETestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return sseResponse(http.StatusOK, sseEvents(body)), nil
			}))
			stream, err := client.Stream(context.Background(), testRequest())
			require.NoError(t, err)
			defer func() { assert.NoError(t, stream.Close()) }()
			chunk, err := stream.Recv()
			assert.Nil(t, chunk)
			var pe *model.ProviderError
			require.ErrorAs(t, err, &pe)
			assert.Equal(t, model.ProviderErrorKindInvalidRequest, pe.Kind())
			assert.False(t, pe.Retryable())
			assert.Equal(t, "provider rejected the request", pe.Message())
		})
	}
}

func codexOverflowHTTPClient(t *testing.T, body string) *Client {
	t.Helper()
	return newSSETestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		response := sseResponse(http.StatusBadRequest, body)
		response.Header.Set("x-request-id", "req_overflow")
		return response, nil
	}))
}

func requireCodexOverflow(t *testing.T, err error) *model.ProviderError {
	t.Helper()
	var pe *model.ProviderError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, model.ProviderErrorKindContextOverflow, pe.Kind())
	assert.Equal(t, "codex", pe.Provider())
	assert.Equal(t, "context_length_exceeded", pe.Code())
	assert.False(t, pe.Retryable())
	assert.Equal(t, "provider rejected the request", pe.Message())
	assert.NotContains(t, err.Error(), "secret-token")
	assert.NotContains(t, err.Error(), "account-1")
	return pe
}
