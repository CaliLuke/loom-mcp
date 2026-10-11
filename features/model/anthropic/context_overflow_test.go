package anthropic

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// Bodies follow https://platform.claude.com/docs/en/build-with-claude/context-windows
// and https://platform.claude.com/docs/en/api/errors.
const anthropicOverflowBody = `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 213462 tokens > 200000 maximum"},"request_id":"req_overflow"}`

// Older Claude diagnostic captured in https://github.com/anthropics/claude-code/issues/52.
// Envelope follows https://platform.claude.com/docs/en/api/errors.
const anthropicLegacyOverflowBody = "{\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"input length and `max_tokens` exceed context limit: 186028 + 20000 > 204698, decrease input length or `max_tokens` and try again\"}}"

func testContextOverflow(t *testing.T) {
	for _, body := range []string{anthropicLegacyOverflowBody, strings.ReplaceAll(anthropicLegacyOverflowBody, "`", "")} {
		t.Run("older Claude "+body, func(t *testing.T) {
			apiErr := anthropicBodyError(t, body)
			response, err := overflowClient(t, &stubMessagesClient{err: apiErr}).Complete(t.Context(), overflowRequest())
			assert.Nil(t, response)
			assert.Equal(t, "messages.new", requireAnthropicOverflow(t, err).Operation())
			assert.ErrorIs(t, err, apiErr)
		})
	}
	t.Run("HTTP error", func(t *testing.T) {
		apiErr := anthropicBodyError(t, anthropicOverflowBody)
		response, err := overflowClient(t, &stubMessagesClient{err: apiErr}).Complete(context.Background(), overflowRequest())
		assert.Nil(t, response)
		pe := requireAnthropicOverflow(t, err)
		assert.Equal(t, 400, pe.HTTPStatus())
		assert.Equal(t, "invalid_request_error", pe.Code())
		assert.Equal(t, "req_overflow", pe.RequestID())
		require.ErrorIs(t, err, apiErr)
	})
	t.Run("context window stop", func(t *testing.T) {
		var message sdk.Message
		require.NoError(t, json.Unmarshal([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"partial"}],"stop_reason":"model_context_window_exceeded","usage":{"input_tokens":199900,"output_tokens":100}}`), &message))
		response, err := overflowClient(t, &stubMessagesClient{resp: &message}).Complete(context.Background(), overflowRequest())
		assert.Nil(t, response)
		assert.Equal(t, "model_context_window_exceeded", requireAnthropicOverflow(t, err).Code())
	})
	for _, tc := range []struct {
		name   string
		body   string
		status int
	}{
		{name: "ordinary 400", body: `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: must be greater than 0"}}`, status: 400},
		{name: "wrong type", body: `{"type":"error","error":{"type":"api_error","message":"prompt is too long: 213462 tokens > 200000 maximum"}}`, status: 400},
		{name: "not a prefix", body: `{"type":"error","error":{"type":"invalid_request_error","message":"invalid field: prompt is too long"}}`, status: 400},
		{name: "wrong status", body: anthropicOverflowBody, status: 500},
		{name: "200 outside stream", body: anthropicOverflowBody, status: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apiErr := anthropicBodyError(t, tc.body)
			apiErr.StatusCode = tc.status
			apiErr.Response.StatusCode = tc.status
			response, err := overflowClient(t, &stubMessagesClient{err: apiErr}).Complete(context.Background(), overflowRequest())
			assert.Nil(t, response)
			require.ErrorIs(t, err, apiErr)
			_, classified := model.AsProviderError(err)
			assert.False(t, classified)
		})
	}
}

func testStreamContextOverflow(t *testing.T) {
	t.Run("HTTP 200 error event", testStreamOverflowErrorEvent)
	for _, phase := range []string{"setup", "receive", "stop"} {
		t.Run(phase, func(t *testing.T) {
			apiErr := anthropicBodyError(t, anthropicOverflowBody)
			var wire *ssestream.Stream[sdk.MessageStreamEventUnion]
			switch phase {
			case "setup":
				wire = ssestream.NewStream[sdk.MessageStreamEventUnion](&testDecoder{}, apiErr)
			case "receive":
				wire = ssestream.NewStream[sdk.MessageStreamEventUnion](&testDecoder{err: apiErr}, nil)
			case "stop":
				wire = ssestream.NewStream[sdk.MessageStreamEventUnion](&testDecoder{events: anthropicConformanceEvents(t,
					`{"type":"message_delta","delta":{"stop_reason":"model_context_window_exceeded","stop_sequence":null},"usage":{"output_tokens":100}}`,
					`{"type":"message_stop"}`,
				)}, nil)
			}
			stream, err := overflowClient(t, &stubMessagesClient{stream: wire}).Stream(context.Background(), overflowRequest())
			if phase == "setup" {
				assert.Nil(t, stream)
				assert.Equal(t, "messages.new stream", requireAnthropicOverflow(t, err).Operation())
				return
			}
			require.NoError(t, err)
			defer func() { assert.NoError(t, stream.Close()) }()
			for err == nil {
				var chunk model.Chunk
				chunk, err = stream.Recv()
				_, stopped := chunk.(model.StopChunk)
				assert.False(t, stopped)
			}
			assert.Equal(t, "messages.new stream", requireAnthropicOverflow(t, err).Operation())
			assert.Nil(t, stream.Response())
		})
	}
}

func testStreamOverflowErrorEvent(t *testing.T) {
	// SSE errors after HTTP 200 are documented at
	// https://platform.claude.com/docs/en/build-with-claude/streaming#error-events
	// and https://platform.claude.com/docs/en/api/errors. Use the SDK's real
	// HTTP decoder so its typed error retains the original successful status.
	for _, tc := range []struct {
		body     string
		overflow bool
	}{
		{anthropicOverflowBody, true},
		{anthropicLegacyOverflowBody, true},
		{`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			body := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\nevent: error\ndata: " + tc.body + "\n\n"
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
			require.NoError(t, err)
			response := &http.Response{StatusCode: http.StatusOK, Request: request, Header: http.Header{"Content-Type": {"text/event-stream"}, "Request-Id": {"req_midstream"}}, Body: io.NopCloser(strings.NewReader(body))}
			wire := ssestream.NewStream[sdk.MessageStreamEventUnion](ssestream.NewDecoder(response), nil)
			stream, err := overflowClient(t, &stubMessagesClient{stream: wire}).Stream(t.Context(), overflowRequest())
			require.NoError(t, err)
			defer func() { assert.NoError(t, stream.Close()) }()
			chunk, err := stream.Recv()
			require.NoError(t, err)
			assert.IsType(t, model.TextChunk{}, chunk)
			chunk, err = stream.Recv()
			assert.Nil(t, chunk)
			if tc.overflow {
				pe := requireAnthropicOverflow(t, err)
				assert.Equal(t, http.StatusOK, pe.HTTPStatus())
				assert.Equal(t, "req_midstream", pe.RequestID())
			} else {
				require.Error(t, err)
				_, classified := model.AsProviderError(err)
				assert.False(t, classified)
			}
			var apiErr *sdk.Error
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, http.StatusOK, apiErr.StatusCode)
			assert.Nil(t, stream.Response())
		})
	}
}

func anthropicBodyError(t *testing.T, body string) *sdk.Error {
	t.Helper()
	apiErr := newAnthropicRateLimitError(t)
	require.NoError(t, json.Unmarshal([]byte(body), apiErr))
	apiErr.StatusCode = http.StatusBadRequest
	apiErr.Response.StatusCode = http.StatusBadRequest
	apiErr.RequestID = "req_overflow"
	return apiErr
}

func requireAnthropicOverflow(t *testing.T, err error) *model.ProviderError {
	t.Helper()
	var pe *model.ProviderError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, model.ProviderErrorKindContextOverflow, pe.Kind())
	assert.Equal(t, "anthropic", pe.Provider())
	assert.False(t, pe.Retryable())
	return pe
}

func overflowClient(t *testing.T, stub *stubMessagesClient) *Client {
	t.Helper()
	client, err := New(stub, Options{DefaultModel: "claude-sonnet-4-5", MaxTokens: 128})
	require.NoError(t, err)
	return client
}

func overflowRequest() *model.Request {
	return &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}}
}
