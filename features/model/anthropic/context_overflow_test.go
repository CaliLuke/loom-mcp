package anthropic

import (
	"context"
	"encoding/json/v2"
	"net/http"
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

func testContextOverflow(t *testing.T) {
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
