package anthropic

import (
	"io"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/testutil"
	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnthropicStreamRetainsThinkingSuppliedAtBlockStart(t *testing.T) {
	events := anthropicConformanceEvents(t,
		`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[]}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"reason","signature":"signed"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":""}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":""}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call","name":"lookup","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":""}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":""}}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":1}}`,
		`{"type":"message_stop"}`,
	)
	client, err := New(&stubMessagesClient{stream: ssestream.NewStream[sdk.MessageStreamEventUnion](&testDecoder{events: events}, nil)}, Options{DefaultModel: "claude", MaxTokens: 128})
	require.NoError(t, err)
	stream, err := client.Stream(t.Context(), &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}})
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, stream.Close())
	})
	chunks := testutil.CollectStreamChunks(t, stream)
	require.Len(t, chunks, 4)
	thinking, ok := chunks[0].(model.ThinkingChunk)
	require.True(t, ok)
	assert.Equal(t, []model.Part{model.ThinkingPart{Text: "reason", Signature: "signed", Index: 0, Final: true}}, thinking.Message.Parts)
	call, ok := chunks[1].(model.ToolCallChunk)
	require.True(t, ok)
	assert.Equal(t, "call", call.ToolCall.ID)
	assert.JSONEq(t, `{}`, string(call.ToolCall.Payload))
}

func TestAnthropicStreamRejectsMissingToolIdentity(t *testing.T) {
	for _, block := range []string{`{"type":"tool_use","name":"lookup","input":{}}`, `{"type":"tool_use","id":"call","input":{}}`} {
		events := anthropicConformanceEvents(t, `{"type":"content_block_start","index":0,"content_block":`+block+`}`)
		client, err := New(&stubMessagesClient{stream: ssestream.NewStream[sdk.MessageStreamEventUnion](&testDecoder{events: events}, nil)}, Options{DefaultModel: "claude", MaxTokens: 128})
		require.NoError(t, err)
		stream, err := client.Stream(t.Context(), &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}})
		require.NoError(t, err)
		t.Cleanup(func() {
			assert.NoError(t, stream.Close())
		})
		chunk, err := stream.Recv()
		require.ErrorContains(t, err, "missing")
		require.NotErrorIs(t, err, io.EOF)
		assert.Nil(t, chunk)
		assert.Nil(t, stream.Response())
	}
}
