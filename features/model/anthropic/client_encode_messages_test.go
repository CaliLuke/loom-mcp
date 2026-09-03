package anthropic

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func TestEncodeMessages_EncodesCitationsPartText(t *testing.T) {
	messages, _, err := encodeMessages([]*model.Message{
		{
			Role: model.ConversationRoleAssistant,
			Parts: []model.Part{
				model.CitationsPart{
					Text: "The answer cites the source.",
					Citations: []model.Citation{
						{Title: "source.pdf", Source: "doc-1"},
					},
				},
			},
		},
	}, nil, newToolUseIDCodec())
	if err != nil {
		t.Fatalf("encodeMessages error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("encoded message count = %d, want 1", len(messages))
	}
	if len(messages[0].Content) != 1 {
		t.Fatalf("encoded content block count = %d, want 1", len(messages[0].Content))
	}
	text := messages[0].Content[0].GetText()
	if text == nil {
		t.Fatal("encoded content block is not text")
	}
	if *text != "The answer cites the source." {
		t.Fatalf("encoded text = %q, want citations text", *text)
	}
}

func TestEncodeMessages_EncodesImagePart(t *testing.T) {
	messages, _, err := encodeMessages([]*model.Message{
		{
			Role: model.ConversationRoleUser,
			Parts: []model.Part{
				model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("png")},
			},
		},
	}, nil, newToolUseIDCodec())
	if err != nil {
		t.Fatalf("encodeMessages error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("encoded message count = %d, want 1", len(messages))
	}
	if len(messages[0].Content) != 1 {
		t.Fatalf("encoded content block count = %d, want 1", len(messages[0].Content))
	}
	image := messages[0].Content[0].OfImage
	if image == nil {
		t.Fatal("encoded content block is not an image")
	}
	source := image.Source
	data := source.GetData()
	if data == nil || *data != "cG5n" {
		t.Fatalf("encoded image data = %v, want base64 png bytes", data)
	}
	mediaType := source.GetMediaType()
	if mediaType == nil || *mediaType != "image/png" {
		t.Fatalf("encoded image media type = %v, want image/png", mediaType)
	}
}

func TestEncodeMessages_EncodesThinkingParts(t *testing.T) {
	messages, _, err := encodeMessages([]*model.Message{
		{
			Role: model.ConversationRoleAssistant,
			Parts: []model.Part{
				model.ThinkingPart{Text: "private reasoning", Signature: "sig"},
				model.ThinkingPart{Redacted: []byte("opaque-redacted")},
			},
		},
	}, nil, newToolUseIDCodec())
	if err != nil {
		t.Fatalf("encodeMessages error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("encoded message count = %d, want 1", len(messages))
	}
	if len(messages[0].Content) != 2 {
		t.Fatalf("encoded content block count = %d, want 2", len(messages[0].Content))
	}

	data, err := json.Marshal(messages[0].Content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, `"type":"thinking"`) ||
		!strings.Contains(got, `"thinking":"private reasoning"`) ||
		!strings.Contains(got, `"signature":"sig"`) {
		t.Fatalf("encoded thinking block = %s, want signed thinking", got)
	}
	if !strings.Contains(got, `"type":"redacted_thinking"`) ||
		!strings.Contains(got, `"data":"opaque-redacted"`) {
		t.Fatalf("encoded redacted thinking block = %s, want redacted data", got)
	}
}

func TestEncodeMessages_CacheCheckpointContract(t *testing.T) {
	tests := []struct {
		name             string
		messages         []*model.Message
		wantConversation int
		wantBlocks       int
		wantSystem       int
		wantCacheBlocks  int
		wantErr          string
	}{
		{
			name: "user cache checkpoint marks preceding block",
			messages: []*model.Message{
				{
					Role: model.ConversationRoleUser,
					Parts: []model.Part{
						model.TextPart{Text: "hi"},
						model.CacheCheckpointPart{},
					},
				},
			},
			wantConversation: 1,
			wantBlocks:       1,
			wantCacheBlocks:  1,
		},
		{
			name: "checkpoint-only message marks preceding message",
			messages: []*model.Message{
				{
					Role:  model.ConversationRoleUser,
					Parts: []model.Part{model.TextPart{Text: "first"}},
				},
				{
					Role:  model.ConversationRoleAssistant,
					Parts: []model.Part{model.CacheCheckpointPart{}},
				},
				{
					Role:  model.ConversationRoleUser,
					Parts: []model.Part{model.TextPart{Text: "hi"}},
				},
			},
			wantConversation: 2,
			wantBlocks:       1,
			wantCacheBlocks:  1,
		},
		{
			name: "message-leading checkpoint marks preceding message",
			messages: []*model.Message{
				{
					Role:  model.ConversationRoleUser,
					Parts: []model.Part{model.TextPart{Text: "first"}},
				},
				{
					Role: model.ConversationRoleAssistant,
					Parts: []model.Part{
						model.CacheCheckpointPart{},
						model.TextPart{Text: "next"},
					},
				},
			},
			wantConversation: 2,
			wantBlocks:       1,
			wantCacheBlocks:  1,
		},
		{
			name: "checkpoint before conversation content errors",
			messages: []*model.Message{
				{
					Role:  model.ConversationRoleUser,
					Parts: []model.Part{model.CacheCheckpointPart{}},
				},
				{
					Role:  model.ConversationRoleUser,
					Parts: []model.Part{model.TextPart{Text: "hi"}},
				},
			},
			wantErr: "anthropic: cache checkpoint has no preceding cacheable conversation block",
		},
		{
			name: "system cache checkpoint marks preceding block",
			messages: []*model.Message{
				{
					Role: model.ConversationRoleSystem,
					Parts: []model.Part{
						model.TextPart{Text: "be brief"},
						model.CacheCheckpointPart{},
					},
				},
				{
					Role:  model.ConversationRoleUser,
					Parts: []model.Part{model.TextPart{Text: "hi"}},
				},
			},
			wantConversation: 1,
			wantBlocks:       1,
			wantSystem:       1,
			wantCacheBlocks:  1,
		},
		{
			name: "checkpoint-only system message marks preceding system message",
			messages: []*model.Message{
				{
					Role:  model.ConversationRoleSystem,
					Parts: []model.Part{model.TextPart{Text: "be brief"}},
				},
				{
					Role:  model.ConversationRoleSystem,
					Parts: []model.Part{model.CacheCheckpointPart{}},
				},
				{
					Role:  model.ConversationRoleUser,
					Parts: []model.Part{model.TextPart{Text: "hi"}},
				},
			},
			wantConversation: 1,
			wantBlocks:       1,
			wantSystem:       1,
			wantCacheBlocks:  1,
		},
		{
			name: "checkpoint before system content errors",
			messages: []*model.Message{
				{
					Role:  model.ConversationRoleSystem,
					Parts: []model.Part{model.CacheCheckpointPart{}},
				},
				{
					Role:  model.ConversationRoleUser,
					Parts: []model.Part{model.TextPart{Text: "hi"}},
				},
			},
			wantErr: "anthropic: cache checkpoint has no preceding cacheable system block",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conversation, system, err := encodeMessages(tt.messages, nil, newToolUseIDCodec())
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Len(t, conversation, tt.wantConversation)
			assert.Len(t, conversation[0].Content, tt.wantBlocks)
			assert.Len(t, system, tt.wantSystem)
			assert.Equal(t, tt.wantCacheBlocks, countCacheControls(t, conversation)+countCacheControls(t, system))
		})
	}
}

func countCacheControls(t *testing.T, value any) int {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return strings.Count(string(data), `"cache_control":{"type":"ephemeral"}`)
}

func TestEncodeMessages_SignedThinkingRoundTripsWithCheckpoint(t *testing.T) {
	conversation, _, err := encodeMessages([]*model.Message{
		{
			Role: model.ConversationRoleAssistant,
			Parts: []model.Part{
				model.ThinkingPart{Text: "private reasoning", Signature: "sig"},
				model.TextPart{Text: "answer"},
				model.CacheCheckpointPart{},
			},
		},
	}, nil, newToolUseIDCodec())
	require.NoError(t, err)
	require.Len(t, conversation, 1)
	require.Len(t, conversation[0].Content, 2)

	data, err := json.Marshal(conversation[0].Content)
	require.NoError(t, err)
	got := string(data)
	assert.Contains(t, got, `"type":"thinking"`)
	assert.Contains(t, got, `"thinking":"private reasoning"`)
	assert.Contains(t, got, `"signature":"sig"`)
}

func TestEncodeMessages_RejectsDocumentPart(t *testing.T) {
	_, _, err := encodeMessages([]*model.Message{
		{
			Role: model.ConversationRoleUser,
			Parts: []model.Part{
				model.DocumentPart{Name: "spec", Format: model.DocumentFormatTXT, Text: "hello"},
			},
		},
	}, nil, newToolUseIDCodec())
	if err == nil {
		t.Fatal("encodeMessages returned nil error")
	}
	if !strings.Contains(err.Error(), "anthropic: unsupported message part model.DocumentPart") {
		t.Fatalf("encodeMessages error = %q, want unsupported document part", err.Error())
	}
}

func TestEncodeMessages_RejectsUnsupportedSystemPart(t *testing.T) {
	_, _, err := encodeMessages([]*model.Message{
		{
			Role: model.ConversationRoleSystem,
			Parts: []model.Part{
				model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("png")},
			},
		},
		{
			Role:  model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{Text: "hi"}},
		},
	}, nil, newToolUseIDCodec())
	if err == nil {
		t.Fatal("encodeMessages returned nil error")
	}
	if !strings.Contains(err.Error(), "anthropic: unsupported system message part model.ImagePart") {
		t.Fatalf("encodeMessages error = %q, want unsupported system image part", err.Error())
	}
}

func TestEncodeMessages_RewritesUnknownToolUseToToolUnavailable(t *testing.T) {
	nameMap := map[string]string{
		tools.ToolUnavailable.String(): sanitizeToolName(tools.ToolUnavailable.String()),
	}
	_, _, err := encodeMessages([]*model.Message{
		{
			Role: model.ConversationRoleAssistant,
			Parts: []model.Part{
				model.ToolUsePart{
					ID:    "tu1",
					Name:  "atlas_read_count_events",
					Input: map[string]any{"from": "2026-02-06T00:00:00Z"},
				},
			},
		},
		{
			Role: model.ConversationRoleUser,
			Parts: []model.Part{
				model.ToolResultPart{
					ToolUseID: "tu1",
					Content:   map[string]any{"error": "unknown tool"},
					IsError:   true,
				},
			},
		},
	}, nameMap, newToolUseIDCodec())
	if err != nil {
		t.Fatalf("encodeMessages error: %v", err)
	}
}

func TestEncodeMessages_ReturnsToolResultMarshalError(t *testing.T) {
	_, _, err := encodeMessages([]*model.Message{
		{
			Role: model.ConversationRoleUser,
			Parts: []model.Part{
				model.ToolResultPart{
					ToolUseID: "tu1",
					Content:   map[string]any{"bad": make(chan int)},
				},
			},
		},
	}, nil, newToolUseIDCodec())
	if err == nil {
		t.Fatal("encodeMessages returned nil error")
	}
	if !strings.Contains(err.Error(), `anthropic: encode tool result "tu1"`) {
		t.Fatalf("encodeMessages error = %q, want tool result context", err.Error())
	}
}
