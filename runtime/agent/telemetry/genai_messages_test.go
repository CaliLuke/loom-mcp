package telemetry

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestGenAIInputMessagesAttrSerializesEveryPartAndRedactsReasoning(t *testing.T) {
	message := &model.Message{
		Role: model.ConversationRoleUser,
		Parts: []model.Part{
			model.TextPart{Text: "hello"},
			model.ToolUsePart{ID: "call-1", Name: "search", Input: map[string]any{"q": "loom"}},
			model.ToolResultPart{ToolUseID: "call-1", Content: map[string]any{"found": true}},
			model.ThinkingPart{Text: "secret reasoning", Signature: "secret signature"},
			model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte{1, 2}},
			model.DocumentPart{Name: "spec", Format: model.DocumentFormatMD, Chunks: []string{"one", "two"}, Context: "reference", Cite: true},
			model.CitationsPart{Text: "supported", Citations: []model.Citation{{Title: "spec"}}},
			model.CacheCheckpointPart{},
		},
	}

	attr, ok, err := GenAIInputMessagesAttr([]*model.Message{nil, message})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, AttrGenAIInputMessages, attr.Key)
	assert.NotContains(t, attr.Value.AsString(), "secret")

	var messages []map[string]any
	require.NoError(t, json.Unmarshal([]byte(attr.Value.AsString()), &messages))
	require.Len(t, messages, 1)
	assert.Equal(t, "user", messages[0]["role"])
	parts, ok := messages[0]["parts"].([]any)
	require.True(t, ok)
	require.Len(t, parts, 7)
	assert.Equal(t, "text", partType(t, parts[0]))
	assert.Equal(t, "tool_call", partType(t, parts[1]))
	assert.Equal(t, "tool_call_response", partType(t, parts[2]))
	assert.Equal(t, "blob", partType(t, parts[3]))
	assert.Equal(t, "text", partType(t, parts[4]))
	assert.Equal(t, "citations", partType(t, parts[5]))
	assert.Equal(t, "cache_checkpoint", partType(t, parts[6]))

	image := parts[3].(map[string]any)
	assert.Equal(t, "image/png", image["mime_type"])
	assert.Equal(t, "AQI=", image["content"])
	document := parts[4].(map[string]any)
	assert.Equal(t, "one\n\ntwo", document["content"])
	assert.Equal(t, []any{"one", "two"}, document["chunks"])
	assert.Equal(t, "text/markdown", document["mime_type"])
	assert.Equal(t, true, document["cite"])
}

func TestGenAIMessagesAttrTreatsPointerPartsLikeValues(t *testing.T) {
	valueMessage := model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
		model.TextPart{Text: "answer"},
		model.ToolUsePart{ID: "call-1", Name: "search"},
		model.ToolResultPart{ToolUseID: "call-1", Content: "found"},
		model.ThinkingPart{Text: "private"},
		model.ImagePart{Format: model.ImageFormatJPEG, Bytes: []byte{1}},
		model.DocumentPart{Name: "doc", URI: "s3://bucket/doc.pdf", Format: model.DocumentFormatPDF},
		model.CitationsPart{Text: "cited"},
		model.CacheCheckpointPart{},
	}}
	pointerMessage := model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{
		&model.TextPart{Text: "answer"},
		&model.ToolUsePart{ID: "call-1", Name: "search"},
		&model.ToolResultPart{ToolUseID: "call-1", Content: "found"},
		&model.ThinkingPart{Text: "private"},
		&model.ImagePart{Format: model.ImageFormatJPEG, Bytes: []byte{1}},
		&model.DocumentPart{Name: "doc", URI: "s3://bucket/doc.pdf", Format: model.DocumentFormatPDF},
		&model.CitationsPart{Text: "cited"},
		&model.CacheCheckpointPart{},
	}}

	valueAttr, valueOK, err := GenAIOutputMessagesAttr([]model.Message{valueMessage}, "stop")
	require.NoError(t, err)
	pointerAttr, pointerOK, err := GenAIOutputMessagesAttr([]model.Message{pointerMessage}, "stop")
	require.NoError(t, err)

	assert.True(t, valueOK)
	assert.True(t, pointerOK)
	assert.JSONEq(t, valueAttr.Value.AsString(), pointerAttr.Value.AsString())
}

func TestGenAIMessagesAttrHandlesEmptyInvalidAndDocumentVariants(t *testing.T) {
	attr, ok, err := GenAIInputMessagesAttr(nil)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, attr.Key)

	attr, ok, err = GenAIInputMessagesAttr([]*model.Message{{Parts: []model.Part{
		model.TextPart{},
		model.ThinkingPart{Text: "private"},
		(*model.TextPart)(nil),
	}}})
	require.NoError(t, err)
	assert.False(t, ok)

	_, ok, err = GenAIInputMessagesAttr([]*model.Message{{Parts: []model.Part{
		model.ToolUsePart{Name: "invalid", Input: func() {}},
	}}})
	assert.False(t, ok)
	require.Error(t, err)

	parts := genAIParts([]model.Part{
		model.DocumentPart{Name: "bytes", Format: model.DocumentFormatPDF, Bytes: []byte{1}},
		model.DocumentPart{Name: "text", Format: model.DocumentFormatTXT, Text: "contents"},
		model.DocumentPart{Name: "invalid"},
	})
	require.Len(t, parts, 3)
	assert.Equal(t, "blob", partType(t, parts[0]))
	assert.Equal(t, "text", partType(t, parts[1]))
	assert.Equal(t, "unknown", partType(t, parts[2]))
}

func TestGenAIMIMETypesCoverSupportedAndUnknownFormats(t *testing.T) {
	imageCases := map[model.ImageFormat]string{
		model.ImageFormatPNG: "image/png", model.ImageFormatJPEG: "image/jpeg", model.ImageFormatGIF: "image/gif", model.ImageFormatWEBP: "image/webp", "unknown": "",
	}
	for format, expected := range imageCases {
		assert.Equal(t, expected, imageMIMEType(format))
	}

	documentCases := map[model.DocumentFormat]string{
		model.DocumentFormatPDF: "application/pdf", model.DocumentFormatCSV: "text/csv", model.DocumentFormatDOC: "application/msword",
		model.DocumentFormatDOCX: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", model.DocumentFormatXLS: "application/vnd.ms-excel",
		model.DocumentFormatXLSX: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", model.DocumentFormatHTML: "text/html",
		model.DocumentFormatTXT: "text/plain", model.DocumentFormatMD: "text/markdown", "unknown": "",
	}
	for format, expected := range documentCases {
		assert.Equal(t, expected, documentMIMEType(format))
	}
}

func partType(t *testing.T, part any) string {
	t.Helper()
	switch value := part.(type) {
	case genAITextPart:
		return value.Type
	case genAIToolCallPart:
		return value.Type
	case genAIToolCallResponsePart:
		return value.Type
	case genAIBlobPart:
		return value.Type
	case genAIURIPart:
		return value.Type
	case map[string]any:
		typeName, ok := value[genAIPartTypeKey].(string)
		require.True(t, ok)
		return typeName
	default:
		require.Failf(t, "unknown part", "type %T", part)
		return ""
	}
}
