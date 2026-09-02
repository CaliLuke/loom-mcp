package model

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type unsupportedTestChunk struct{}

func (unsupportedTestChunk) Kind() string { return "unsupported" }

func (unsupportedTestChunk) isChunk() {}

func FuzzValidateChunkShape(f *testing.F) {
	f.Add(uint8(0), "text", int16(1))
	f.Add(uint8(7), "end_turn", int16(0))
	f.Add(uint8(9), "", int16(-1))
	f.Fuzz(func(t *testing.T, variant uint8, content string, count int16) {
		message := Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: content}}}
		var chunk Chunk
		switch variant % 11 {
		case 0:
			chunk = TextChunk{Message: message}
		case 1:
			chunk = ThinkingChunk{Message: Message{Role: ConversationRoleAssistant, Parts: []Part{ThinkingPart{Text: content}}}}
		case 2:
			chunk = ToolCallChunk{ToolCall: ToolCall{Name: tools.Ident(content), Payload: rawjson.Message(`{}`)}}
		case 3:
			chunk = ToolCallDeltaChunk{Delta: ToolCallDelta{Name: tools.Ident(content), ID: "id", Delta: content}}
		case 4:
			chunk = CompletionChunk{Completion: Completion{Name: content, Payload: rawjson.Message(content)}}
		case 5:
			chunk = CompletionDeltaChunk{Delta: CompletionDelta{Name: content, Delta: content}}
		case 6:
			chunk = UsageChunk{Usage: TokenUsage{InputTokens: int(count), TotalTokens: int(count)}}
		case 7:
			chunk = StopChunk{Reason: content, OutputLimited: count%2 != 0}
		case 8:
			chunk = unsupportedTestChunk{}
		case 9:
			chunk = nil
		case 10:
			var typedNil *TextChunk
			chunk = typedNil
		}
		_ = validateChunkShape(chunk)
	})
}

func TestValidateChunkShapeRejectsUnsupportedOrInvalidVariants(t *testing.T) {
	var typedNil *TextChunk
	tests := []Chunk{
		nil,
		typedNil,
		unsupportedTestChunk{},
		TextChunk{Message: Message{Role: ConversationRoleAssistant}},
		TextChunk{Message: Message{Role: ConversationRoleUser, Parts: []Part{TextPart{Text: "wrong role"}}}},
		TextChunk{Message: Message{Role: ConversationRoleAssistant, Parts: []Part{ThinkingPart{Text: "not text"}}}},
		ThinkingChunk{Message: Message{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "not thinking"}}}},
		ThinkingChunk{Message: Message{Role: ConversationRoleUser, Parts: []Part{ThinkingPart{Text: "wrong role"}}}},
		CompletionChunk{Completion: Completion{Name: "name"}},
		UsageChunk{Usage: TokenUsage{InputTokens: -1}},
		StopChunk{},
	}
	for _, chunk := range tests {
		if err := validateChunkShape(chunk); err == nil {
			t.Fatalf("validateChunkShape(%T) unexpectedly succeeded", chunk)
		}
	}
}
