package anthropic

import (
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
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
	}, nil)
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
	}, nil)
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

func TestEncodeMessages_RejectsDocumentPart(t *testing.T) {
	_, _, err := encodeMessages([]*model.Message{
		{
			Role: model.ConversationRoleUser,
			Parts: []model.Part{
				model.DocumentPart{Name: "spec", Format: model.DocumentFormatTXT, Text: "hello"},
			},
		},
	}, nil)
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
	}, nil)
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
	}, nameMap)
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
	}, nil)
	if err == nil {
		t.Fatal("encodeMessages returned nil error")
	}
	if !strings.Contains(err.Error(), `anthropic: encode tool result "tu1"`) {
		t.Fatalf("encodeMessages error = %q, want tool result context", err.Error())
	}
}
