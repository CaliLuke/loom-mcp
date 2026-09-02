package model

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func FuzzValidateChunkShape(f *testing.F) {
	f.Add(ChunkTypeText, uint16(1), "text")
	f.Add(ChunkTypeStop, uint16(1<<7), "end_turn")
	f.Add("unknown", uint16(0), "")
	f.Fuzz(func(t *testing.T, chunkType string, fields uint16, content string) {
		chunk := Chunk{Type: chunkType, OutputLimited: fields&(1<<8) != 0}
		if fields&(1<<0) != 0 {
			chunk.Message = &Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: content}}}
		}
		if fields&(1<<1) != 0 {
			chunk.Thinking = content
		}
		if fields&(1<<2) != 0 {
			chunk.ToolCall = &ToolCall{Name: tools.Ident(content), Payload: rawjson.Message(`{}`)}
		}
		if fields&(1<<3) != 0 {
			chunk.ToolCallDelta = &ToolCallDelta{Name: tools.Ident(content), ID: "id", Delta: content}
		}
		if fields&(1<<4) != 0 {
			chunk.Completion = &Completion{Name: content, Payload: rawjson.Message(`{}`)}
		}
		if fields&(1<<5) != 0 {
			chunk.CompletionDelta = &CompletionDelta{Name: content, Delta: content}
		}
		if fields&(1<<6) != 0 {
			chunk.UsageDelta = &TokenUsage{InputTokens: int(fields & 7), TotalTokens: int(fields & 7)}
		}
		if fields&(1<<7) != 0 {
			chunk.StopReason = content
		}
		_ = validateChunkShape(chunk)
	})
}

func TestValidateChunkShapeRejectsOpenVariantConflicts(t *testing.T) {
	text := &Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "text"}}}
	usage := &TokenUsage{InputTokens: 1, TotalTokens: 2}
	tests := []Chunk{
		{},
		{Type: "unknown"},
		{Type: ChunkTypeText},
		{Type: ChunkTypeText, Message: text, StopReason: "extra"},
		{Type: ChunkTypeText, Message: text, OutputLimited: true},
		{Type: ChunkTypeThinking},
		{Type: ChunkTypeThinking, Message: text, Thinking: "duplicate"},
		{Type: ChunkTypeToolCall},
		{Type: ChunkTypeToolCallDelta},
		{Type: ChunkTypeCompletion},
		{Type: ChunkTypeCompletion, Completion: &Completion{Name: "name"}},
		{Type: ChunkTypeCompletionDelta},
		{Type: ChunkTypeUsage},
		{Type: ChunkTypeUsage, UsageDelta: usage},
		{Type: ChunkTypeStop},
		{Type: ChunkTypeStop, StopReason: "stop", Message: text},
	}
	for _, chunk := range tests {
		if err := validateChunkShape(chunk); err == nil {
			t.Fatalf("validateChunkShape(%+v) unexpectedly succeeded", chunk)
		}
	}
}
