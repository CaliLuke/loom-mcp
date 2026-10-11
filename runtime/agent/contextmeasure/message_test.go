package contextmeasure

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageSizeMatchesRequestParts(t *testing.T) {
	message := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{
		&model.TextPart{Text: "é"}, model.CitationsPart{Text: "cite"}, model.ThinkingPart{Text: "reason", Signature: "ignored"},
		model.ImagePart{Format: "png", Bytes: []byte{1, 2, 3}},
		model.DocumentPart{Name: "doc", Context: "ctx", Text: "body", Chunks: []string{"one", "two"}, Bytes: []byte{1, 2, 3, 4, 5}},
		model.DocumentPart{URI: "https://example.test/document"},
		model.ToolUsePart{ID: "id", Name: "tool", Input: map[string]any{"x": 1}},
		model.ToolResultPart{ToolUseID: "id", Content: "result"}, model.CacheCheckpointPart{},
	}, Meta: map[string]any{"ignored": "metadata"}}
	got, err := MeasureMessages([]*model.Message{message, nil})
	require.NoError(t, err)
	facts, err := Measure(&model.Request{Messages: []*model.Message{message}}, model.ContextLimits{}, nil)
	require.NoError(t, err)
	assert.Equal(t, model.ConversationRoleUser, got.Role)
	assert.Equal(t, facts.TextBytes, got.Bytes)
	assert.Equal(t, 3003, got.NonText)
	assert.Equal(t, facts.NonText, got.NonText)
	_, err = MeasureMessages([]*model.Message{{Parts: []model.Part{(*model.ImagePart)(nil)}}})
	assert.Error(t, err)
}
