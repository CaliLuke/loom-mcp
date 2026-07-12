package anthropic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
)

func TestToolUseIDCodec_EncodeDecodeRoundTrip(t *testing.T) {
	tests := []struct {
		name        string
		id          string
		wantEncoded string
		remapped    bool
	}{
		{
			name:        "run scoped slash id is remapped",
			id:          "run-1/turn-2/attempt-0/atlas.read.count/0",
			wantEncoded: "t1",
			remapped:    true,
		},
		{
			name:        "graph loop hash id is remapped",
			id:          "run-1/turn-2/attempt-0/tool/0#3",
			wantEncoded: "t1",
			remapped:    true,
		},
		{
			name:        "over 64 char id is remapped",
			id:          strings.Repeat("a", 65),
			wantEncoded: "t1",
			remapped:    true,
		},
		{
			name:        "provider safe id passes through",
			id:          "toolu_01A09q90qw90lq917835lq9",
			wantEncoded: "toolu_01A09q90qw90lq917835lq9",
			remapped:    false,
		},
		{
			name:        "empty id stays empty",
			id:          "",
			wantEncoded: "",
			remapped:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codec := newToolUseIDCodec()
			encoded := codec.encode(tt.id)
			assert.Equal(t, tt.wantEncoded, encoded)
			if encoded != "" {
				assert.True(t, isProviderSafeToolUseID(encoded), "encoded id %q must be provider safe", encoded)
			}
			if tt.remapped {
				assert.NotEqual(t, tt.id, encoded)
			}
			assert.Equal(t, tt.id, codec.decode(encoded), "decode must restore the original id")
		})
	}
}

func TestToolUseIDCodec_StableMappingAndDistinctIDs(t *testing.T) {
	codec := newToolUseIDCodec()
	first := "run-1/turn-1/attempt-0/tool/0"
	second := "run-1/turn-1/attempt-0/tool/0#1"

	assert.Equal(t, "t1", codec.encode(first))
	assert.Equal(t, "t1", codec.encode(first), "same canonical id must map to the same provider id")
	assert.Equal(t, "t2", codec.encode(second), "distinct canonical ids must map to distinct provider ids")
	assert.Equal(t, first, codec.decode("t1"))
	assert.Equal(t, second, codec.decode("t2"))
	assert.Equal(t, "unknown-id", codec.decode("unknown-id"), "unmapped ids pass through decode unchanged")
}

func TestEncodeMessages_SanitizesToolUseIDsAndKeepsPairing(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		wantID string
	}{
		{
			name:   "run scoped slash id",
			id:     "run-1/turn-2/attempt-0/test.tool/0",
			wantID: "t1",
		},
		{
			name:   "graph loop hash id",
			id:     "run-1/turn-2/attempt-0/test.tool/0#4",
			wantID: "t1",
		},
		{
			name:   "over 64 char id",
			id:     strings.Repeat("x", 70),
			wantID: "t1",
		},
		{
			name:   "provider safe id passes through",
			id:     "toolu_abc123",
			wantID: "toolu_abc123",
		},
	}
	nameMap := map[string]string{"test.tool": "tool"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages, _, err := encodeMessages([]*model.Message{
				{
					Role: model.ConversationRoleAssistant,
					Parts: []model.Part{
						model.ToolUsePart{ID: tt.id, Name: "test.tool", Input: map[string]any{"x": 1}},
					},
				},
				{
					Role: model.ConversationRoleUser,
					Parts: []model.Part{
						model.ToolResultPart{ToolUseID: tt.id, Content: "ok"},
					},
				},
			}, nameMap, newToolUseIDCodec())
			require.NoError(t, err)
			require.Len(t, messages, 2)

			toolUse := messages[0].Content[0].OfToolUse
			require.NotNil(t, toolUse, "first block must be tool_use")
			assert.Equal(t, tt.wantID, toolUse.ID)
			assert.True(t, isProviderSafeToolUseID(toolUse.ID))

			toolResult := messages[1].Content[0].OfToolResult
			require.NotNil(t, toolResult, "second block must be tool_result")
			assert.Equal(t, tt.wantID, toolResult.ToolUseID)
			assert.Equal(t, toolUse.ID, toolResult.ToolUseID, "tool_use and tool_result must stay paired")
		})
	}
}

func TestComplete_RestoresCanonicalToolUseIDs(t *testing.T) {
	const runScopedID = "run-1/turn-2/attempt-0/test.tool/0"

	stub := &stubMessagesClient{}
	cl, err := New(stub, Options{DefaultModel: "claude-3.5-sonnet", MaxTokens: 128})
	require.NoError(t, err)

	req := &model.Request{
		Messages: []*model.Message{
			{
				Role:  model.ConversationRoleUser,
				Parts: []model.Part{model.TextPart{Text: "call tool"}},
			},
			{
				Role: model.ConversationRoleAssistant,
				Parts: []model.Part{
					model.ToolUsePart{ID: runScopedID, Name: "test.tool", Input: map[string]any{"x": 1}},
				},
			},
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.ToolResultPart{ToolUseID: runScopedID, Content: "ok"},
				},
			},
		},
		Tools: []*model.ToolDefinition{
			{
				Name:        "test.tool",
				Description: "test tool",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
		},
	}

	// The provider echoes the substituted id back in a new tool_use block.
	stub.resp = &sdk.Message{
		Content: []sdk.ContentBlockUnion{
			{
				Type:  "tool_use",
				Name:  "tool",
				ID:    "t1",
				Input: json.RawMessage(`{"x":2}`),
			},
		},
		StopReason: sdk.StopReasonToolUse,
	}

	resp, err := cl.Complete(context.Background(), req)
	require.NoError(t, err)

	// The request forwarded to the provider must not contain the raw run-scoped id.
	sent, err := json.Marshal(stub.lastParams.Messages)
	require.NoError(t, err)
	assert.NotContains(t, string(sent), runScopedID)
	assert.Contains(t, string(sent), `"id":"t1"`)
	assert.Contains(t, string(sent), `"tool_use_id":"t1"`)

	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, runScopedID, resp.ToolCalls[0].ID, "decode must restore the canonical id")
}

func TestAnthropicStreamer_RestoresCanonicalToolUseID(t *testing.T) {
	const runScopedID = "run-1/turn-2/attempt-0/toolset.tool/0"

	codec := newToolUseIDCodec()
	require.Equal(t, "t1", codec.encode(runScopedID))

	toolStart := sdk.MessageStreamEventUnion{}
	require.NoError(t, json.Unmarshal([]byte(`{
  "type": "content_block_start",
  "index": 0,
  "content_block": { "type": "tool_use", "id": "t1", "name": "tool_a" }
}`), &toolStart))

	toolDelta := sdk.MessageStreamEventUnion{}
	require.NoError(t, json.Unmarshal([]byte(`{
  "type": "content_block_delta",
  "index": 0,
  "delta": { "type": "input_json_delta", "partial_json": "{\"x\":1}" }
}`), &toolDelta))

	toolStop := sdk.MessageStreamEventUnion{}
	require.NoError(t, json.Unmarshal([]byte(`{
  "type": "content_block_stop",
  "index": 0
}`), &toolStop))

	stop := sdk.MessageStreamEventUnion{}
	require.NoError(t, json.Unmarshal([]byte(`{
  "type": "message_stop"
}`), &stop))

	events := []ssestream.Event{
		{Type: "content_block_start", Data: mustJSON(toolStart)},
		{Type: "content_block_delta", Data: mustJSON(toolDelta)},
		{Type: "content_block_stop", Data: mustJSON(toolStop)},
		{Type: "message_stop", Data: mustJSON(stop)},
	}

	dec := &testDecoder{events: events}
	stream := ssestream.NewStream[sdk.MessageStreamEventUnion](dec, nil)
	nameMap := map[string]string{"tool_a": "toolset.tool"}

	s := newAnthropicStreamer(context.Background(), stream, "", "", nameMap, codec)
	defer func() {
		if err := s.Close(); err != nil {
			t.Errorf("close streamer: %v", err)
		}
	}()

	var deltaID, callID string
	for {
		ch, err := s.Recv()
		if err != nil {
			break
		}
		switch ch.Type {
		case model.ChunkTypeToolCallDelta:
			deltaID = ch.ToolCallDelta.ID
		case model.ChunkTypeToolCall:
			callID = ch.ToolCall.ID
		}
	}

	assert.Equal(t, runScopedID, deltaID, "tool call delta must carry the canonical id")
	assert.Equal(t, runScopedID, callID, "final tool call must carry the canonical id")
}
