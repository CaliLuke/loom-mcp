package anthropic

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func FuzzToolFragmentPayload(f *testing.F) {
	f.Add(`{"query":`, `"loom"}`)
	f.Add("", "")
	f.Add("{", "")
	f.Add(`{"nested":[1,`, `2]}`)

	f.Fuzz(func(t *testing.T, left, right string) {
		if len(left)+len(right) > 1<<20 {
			return
		}
		buffer := toolBuffer{fragments: []string{left, right}}
		payload, err := decodeToolPayload(buffer.finalInput())
		trimmed := strings.TrimSpace(left + right)
		if trimmed == "" {
			trimmed = "{}"
		}
		if !jsontext.Value([]byte(trimmed)).IsValid() {
			require.Error(t, err)
			return
		}
		require.NoError(t, err)
		require.Equal(t, trimmed, string(payload))
	})
}

func TestAnthropicChunkProcessorRejectsMalformedToolFragments(t *testing.T) {
	processor := newAnthropicChunkProcessor(
		func(model.Chunk) error { return nil },
		nil,
		"",
		model.ModelClassDefault,
		nil,
		newToolUseIDCodec(),
	)
	processor.toolBlocks[1] = &toolBuffer{
		name:      "lookup",
		id:        "call-1",
		fragments: []string{"{"},
	}

	err := processor.emitFinalToolCall(1)
	require.EqualError(t, err, `anthropic stream: tool call "call-1" payload: invalid JSON`)
	require.NotContains(t, processor.toolBlocks, 1)
}

// testDecoder feeds a fixed sequence of events to the ssestream.Stream.
type testDecoder struct {
	events       []ssestream.Event
	i            int
	err          error
	closeErr     error
	closeErrOnce bool
	closeCalls   atomic.Int32
}

func (d *testDecoder) Event() ssestream.Event { return d.events[d.i-1] }

func (d *testDecoder) Next() bool {
	if d.err != nil {
		return false
	}
	if d.i >= len(d.events) {
		return false
	}
	d.i++
	return true
}

func (d *testDecoder) Close() error {
	if d.closeErrOnce && d.closeCalls.Add(1) > 1 {
		return nil
	}
	return d.closeErr
}
func (d *testDecoder) Err() error { return d.err }

func TestAnthropicStreamer_TextAndToolCall(t *testing.T) {
	// Build a minimal text delta and tool_use JSON sequence.
	textDelta := sdk.MessageStreamEventUnion{
		Type: "content_block_delta",
	}
	if err := json.Unmarshal([]byte(`{
  "type": "content_block_delta",
  "index": 0,
  "delta": { "type": "text_delta", "text": "hello" }
}`), &textDelta); err != nil {
		t.Fatalf("unmarshal text delta: %v", err)
	}

	toolStart := sdk.MessageStreamEventUnion{}
	if err := json.Unmarshal([]byte(`{
  "type": "content_block_start",
  "index": 1,
  "content_block": { "type": "tool_use", "id": "t1", "name": "tool_a" }
}`), &toolStart); err != nil {
		t.Fatalf("unmarshal tool start: %v", err)
	}

	toolDelta := sdk.MessageStreamEventUnion{}
	if err := json.Unmarshal([]byte(`{
  "type": "content_block_delta",
  "index": 1,
  "delta": { "type": "input_json_delta", "partial_json": "{\"x\":1}" }
}`), &toolDelta); err != nil {
		t.Fatalf("unmarshal tool delta: %v", err)
	}

	toolStop := sdk.MessageStreamEventUnion{}
	if err := json.Unmarshal([]byte(`{
  "type": "content_block_stop",
  "index": 1
}`), &toolStop); err != nil {
		t.Fatalf("unmarshal tool stop: %v", err)
	}

	stop := sdk.MessageStreamEventUnion{}
	if err := json.Unmarshal([]byte(`{
  "type": "message_stop"
}`), &stop); err != nil {
		t.Fatalf("unmarshal message stop: %v", err)
	}

	events := []ssestream.Event{
		{Type: "content_block_delta", Data: mustJSON(textDelta)},
		{Type: "content_block_start", Data: mustJSON(toolStart)},
		{Type: "content_block_delta", Data: mustJSON(toolDelta)},
		{Type: "content_block_stop", Data: mustJSON(toolStop)},
		{Type: "message_stop", Data: mustJSON(stop)},
	}

	dec := &testDecoder{events: events}
	stream := ssestream.NewStream[sdk.MessageStreamEventUnion](dec, nil)
	nameMap := map[string]string{"tool_a": "toolset.tool"}

	s := newAnthropicStreamer(context.Background(), stream, "", "", nameMap, newToolUseIDCodec())
	defer func() {
		_ = s.Close()
	}()

	var chunks []model.Chunk
	for {
		ch, err := s.Recv()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("unexpected context error: %v", err)
			}
			break
		}
		chunks = append(chunks, ch)
	}

	if len(chunks) == 0 {
		t.Fatalf("expected chunks, got none")
	}

	var sawText, sawTool bool
	for _, ch := range chunks {
		switch ch.Type {
		case model.ChunkTypeText:
			sawText = true
		case model.ChunkTypeToolCall:
			sawTool = true
			if ch.ToolCall == nil {
				t.Fatalf("tool chunk missing ToolCall")
			}
			if string(ch.ToolCall.Name) != "toolset.tool" {
				t.Fatalf("unexpected tool name %q", ch.ToolCall.Name)
			}
		}
	}
	if !sawText {
		t.Fatalf("expected text chunk")
	}
	if !sawTool {
		t.Fatalf("expected tool_call chunk")
	}
}

func TestAnthropicStreamerRejectsMalformedEventJSON(t *testing.T) {
	stream := ssestream.NewStream[sdk.MessageStreamEventUnion](&testDecoder{
		events: []ssestream.Event{{Type: "message_start", Data: []byte("{")}},
	}, nil)
	s := newAnthropicStreamer(context.Background(), stream, "", "", nil, newToolUseIDCodec())
	defer func() {
		if err := s.Close(); err != nil {
			t.Fatalf("close stream: %v", err)
		}
	}()

	_, err := s.Recv()
	if err == nil {
		t.Fatal("expected malformed event error")
	}
}

func TestAnthropicStreamerRejectsEOFBeforeMessageStop(t *testing.T) {
	textDelta := sdk.MessageStreamEventUnion{}
	if err := json.Unmarshal([]byte("{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}"), &textDelta); err != nil {
		t.Fatalf("unmarshal text delta: %v", err)
	}
	stream := ssestream.NewStream[sdk.MessageStreamEventUnion](&testDecoder{
		events: []ssestream.Event{{Type: "content_block_delta", Data: mustJSON(textDelta)}},
	}, nil)
	s := newAnthropicStreamer(context.Background(), stream, "", "", nil, newToolUseIDCodec())
	defer func() {
		if err := s.Close(); err != nil {
			t.Fatalf("close stream: %v", err)
		}
	}()

	chunk, err := s.Recv()
	if err != nil {
		t.Fatalf("receive text: %v", err)
	}
	if chunk.Type != model.ChunkTypeText {
		t.Fatalf("chunk type = %q, want %q", chunk.Type, model.ChunkTypeText)
	}
	_, err = s.Recv()
	if err == nil || err.Error() != "anthropic: stream ended before message_stop" {
		t.Fatalf("error = %v, want premature EOF error", err)
	}
}

func TestAnthropicStreamer_ThinkingBlocks(t *testing.T) {
	thinkingStart := sdk.MessageStreamEventUnion{}
	if err := json.Unmarshal([]byte(`{
  "type": "content_block_start",
  "index": 0,
  "content_block": { "type": "thinking", "thinking": "" }
}`), &thinkingStart); err != nil {
		t.Fatalf("unmarshal thinking start: %v", err)
	}

	thinkingDelta := sdk.MessageStreamEventUnion{}
	if err := json.Unmarshal([]byte(`{
  "type": "content_block_delta",
  "index": 0,
  "delta": { "type": "thinking_delta", "thinking": "private reasoning" }
}`), &thinkingDelta); err != nil {
		t.Fatalf("unmarshal thinking delta: %v", err)
	}

	signatureDelta := sdk.MessageStreamEventUnion{}
	if err := json.Unmarshal([]byte(`{
  "type": "content_block_delta",
  "index": 0,
  "delta": { "type": "signature_delta", "signature": "sig" }
}`), &signatureDelta); err != nil {
		t.Fatalf("unmarshal signature delta: %v", err)
	}

	thinkingStop := sdk.MessageStreamEventUnion{}
	if err := json.Unmarshal([]byte(`{
  "type": "content_block_stop",
  "index": 0
}`), &thinkingStop); err != nil {
		t.Fatalf("unmarshal thinking stop: %v", err)
	}

	redactedStart := sdk.MessageStreamEventUnion{}
	if err := json.Unmarshal([]byte(`{
  "type": "content_block_start",
  "index": 1,
  "content_block": { "type": "redacted_thinking", "data": "opaque-redacted" }
}`), &redactedStart); err != nil {
		t.Fatalf("unmarshal redacted start: %v", err)
	}

	redactedStop := sdk.MessageStreamEventUnion{}
	if err := json.Unmarshal([]byte(`{
  "type": "content_block_stop",
  "index": 1
}`), &redactedStop); err != nil {
		t.Fatalf("unmarshal redacted stop: %v", err)
	}

	stop := sdk.MessageStreamEventUnion{}
	if err := json.Unmarshal([]byte(`{
  "type": "message_stop"
}`), &stop); err != nil {
		t.Fatalf("unmarshal message stop: %v", err)
	}

	events := []ssestream.Event{
		{Type: "content_block_start", Data: mustJSON(thinkingStart)},
		{Type: "content_block_delta", Data: mustJSON(thinkingDelta)},
		{Type: "content_block_delta", Data: mustJSON(signatureDelta)},
		{Type: "content_block_stop", Data: mustJSON(thinkingStop)},
		{Type: "content_block_start", Data: mustJSON(redactedStart)},
		{Type: "content_block_stop", Data: mustJSON(redactedStop)},
		{Type: "message_stop", Data: mustJSON(stop)},
	}

	stream := ssestream.NewStream[sdk.MessageStreamEventUnion](&testDecoder{events: events}, nil)
	s := newAnthropicStreamer(context.Background(), stream, "", "", nil, newToolUseIDCodec())
	defer func() {
		_ = s.Close()
	}()

	var thinkingDeltas int
	var finalSigned, finalRedacted *model.ThinkingPart
	for {
		ch, err := s.Recv()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("unexpected context error: %v", err)
			}
			break
		}
		if ch.Type != model.ChunkTypeThinking || ch.Message == nil || len(ch.Message.Parts) == 0 {
			continue
		}
		part, ok := ch.Message.Parts[0].(model.ThinkingPart)
		if !ok {
			t.Fatalf("thinking chunk part = %T, want ThinkingPart", ch.Message.Parts[0])
		}
		if !part.Final {
			thinkingDeltas++
			continue
		}
		if part.Signature != "" {
			cp := part
			finalSigned = &cp
			continue
		}
		if len(part.Redacted) > 0 {
			cp := part
			finalRedacted = &cp
		}
	}

	if thinkingDeltas != 1 {
		t.Fatalf("thinking delta count = %d, want 1", thinkingDeltas)
	}
	if finalSigned == nil || finalSigned.Text != "private reasoning" || finalSigned.Signature != "sig" {
		t.Fatalf("final signed thinking = %+v, want text/signature", finalSigned)
	}
	if finalRedacted == nil || string(finalRedacted.Redacted) != "opaque-redacted" {
		t.Fatalf("final redacted thinking = %+v, want redacted data", finalRedacted)
	}
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
