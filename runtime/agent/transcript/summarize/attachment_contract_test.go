package summarize

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummaryAttachmentAnnotationsUseRecordedBytesAndDecimalRounding(t *testing.T) {
	for _, tc := range []struct {
		name  string
		part  model.Part
		bytes int
		size  string
	}{
		{"KB round even down", model.ImagePart{Format: "png", Bytes: make([]byte, 1050)}, 1050, "1.0 KB"},
		{"KB round even up", model.ImagePart{Format: "png", Bytes: make([]byte, 1150)}, 1150, "1.2 KB"},
		{"KB round up", model.ImagePart{Format: "png", Bytes: make([]byte, 1051)}, 1051, "1.1 KB"},
		{"MB round even down", model.DocumentPart{Name: "source", Format: "pdf", Bytes: make([]byte, 1050000)}, 1050000, "1.0 MB"},
		{"MB round up", model.DocumentPart{Name: "source", Format: "pdf", Bytes: make([]byte, 1050001)}, 1050001, "1.1 MB"},
		{"URI", model.DocumentPart{Name: "source", Format: "pdf", URI: "https://example.com/doc.pdf"}, 27, "27 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			evidence, err := RenderInput(Input{Entries: []index.MessageEntry{{ID: 1, Kind: index.Message, Hash: "content"}}, Contents: map[runstore.Hash][]byte{"content": encoded(t, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{tc.part}})}, From: 1, To: 2, CurrentPos: 1})
			require.NoError(t, err)
			require.Len(t, evidence.Units, 1)
			assert.Contains(t, evidence.Units[0].Text, tc.size)
			require.Len(t, evidence.Annotations, 1)
			assert.Equal(t, tc.bytes, evidence.Annotations[0].Bytes)
			assert.Equal(t, index.MessageID(1), evidence.Annotations[0].MessageID)
			assert.Equal(t, 1, evidence.Annotations[0].Part)
			assert.Nil(t, evidence.Units[0].Attachment)
		})
	}
}

func TestSummaryInlineToolCallsRespectHiddenBookkeeping(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		t.Run(map[bool]string{false: "visible", true: "hidden"}[hidden], func(t *testing.T) {
			entries := []index.MessageEntry{{ID: 1, Kind: index.Message, Hash: "content"}}
			if hidden {
				entries = append(entries, index.MessageEntry{ID: 2, Kind: index.Structural, ToolUseIDs: []string{"call"}, Structural: &index.StructuralEntry{Kind: index.HideBookkeeping, ProtocolVersion: 5}})
			}
			evidence, err := RenderInput(Input{Entries: entries, Contents: map[runstore.Hash][]byte{"content": encoded(t, &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
				model.CitationsPart{Text: "evidence text"}, model.ToolUsePart{ID: "call", Name: "lookup", Input: map[string]any{"query": "visible"}}, model.ToolResultPart{ToolUseID: "call", Content: map[string]any{"answer": "fact"}},
			}})}, From: 1, To: 3, CurrentPos: 1})
			require.NoError(t, err)
			if hidden {
				require.Len(t, evidence.Units, 1)
				assert.Equal(t, "evidence text", evidence.Units[0].Text)
				return
			}
			require.Len(t, evidence.Units, 3)
			assert.Equal(t, "evidence text", evidence.Units[0].Text)
			assert.JSONEq(t, `{"query":"visible"}`, evidence.Units[1].Text)
			assert.Equal(t, "call", evidence.Units[1].ToolUseID)
			assert.JSONEq(t, `{"answer":"fact"}`, evidence.Units[2].Text)
			assert.True(t, evidence.Units[2].IsResult)
		})
	}
}
