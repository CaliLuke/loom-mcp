package openai_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestOpenAIDocumentRejectsUnsupportedInputBeforeIO(t *testing.T) {
	tests := []struct {
		name string
		part model.DocumentPart
		role model.ConversationRole
		want string
	}{
		{"text source", model.DocumentPart{Name: "private-name", Format: model.DocumentFormatPDF, Text: "private-content"}, model.ConversationRoleUser, "document bytes are required"},
		{"chunks source", model.DocumentPart{Name: "private-name", Format: model.DocumentFormatPDF, Chunks: []string{"private-content"}}, model.ConversationRoleUser, "document bytes are required"},
		{"URI source", model.DocumentPart{Name: "private-name", Format: model.DocumentFormatPDF, URI: "https://private.example/file"}, model.ConversationRoleUser, "document bytes are required"},
		{"no source", model.DocumentPart{Name: "private-name", Format: model.DocumentFormatPDF}, model.ConversationRoleUser, "document bytes are required"},
		{"bytes and text", model.DocumentPart{Name: "private-name", Format: model.DocumentFormatPDF, Bytes: documentPDF, Text: "private-content"}, model.ConversationRoleUser, "only a byte source"},
		{"bytes and chunks", model.DocumentPart{Name: "private-name", Format: model.DocumentFormatPDF, Bytes: documentPDF, Chunks: []string{"private-content"}}, model.ConversationRoleUser, "only a byte source"},
		{"bytes and URI", model.DocumentPart{Name: "private-name", Format: model.DocumentFormatPDF, Bytes: documentPDF, URI: "https://private.example/file"}, model.ConversationRoleUser, "only a byte source"},
		{"context", model.DocumentPart{Name: "private-name", Format: model.DocumentFormatPDF, Bytes: documentPDF, Context: "private-context"}, model.ConversationRoleUser, "document context"},
		{"citations", model.DocumentPart{Name: "private-name", Format: model.DocumentFormatPDF, Bytes: documentPDF, Cite: true}, model.ConversationRoleUser, "document citations"},
		{"empty name", model.DocumentPart{Format: model.DocumentFormatPDF, Bytes: documentPDF}, model.ConversationRoleUser, "document name"},
		{"invalid UTF-8 name", model.DocumentPart{Name: "bad\xff", Format: model.DocumentFormatPDF, Bytes: documentPDF}, model.ConversationRoleUser, "document name"},
		{"system", model.DocumentPart{Name: "private-name", Format: model.DocumentFormatPDF, Bytes: documentPDF}, model.ConversationRoleSystem, "only supported in user messages"},
		{"assistant", model.DocumentPart{Name: "private-name", Format: model.DocumentFormatPDF, Bytes: documentPDF}, model.ConversationRoleAssistant, "only supported in user messages"},
		{"tool", model.DocumentPart{Name: "private-name", Format: model.DocumentFormatPDF, Bytes: documentPDF}, "tool", "only supported in user messages"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertDocumentRejectedBeforeIO(t, tt.part, tt.role, tt.want)
		})
	}
}

func TestOpenAIDocumentRejectsUnsupportedFormatsBeforeIO(t *testing.T) {
	for _, format := range []model.DocumentFormat{"", "PDF", " pdf", "unknown", model.DocumentFormatCSV, model.DocumentFormatDOC, model.DocumentFormatDOCX, model.DocumentFormatXLS, model.DocumentFormatXLSX, model.DocumentFormatHTML, model.DocumentFormatTXT, model.DocumentFormatMD} {
		t.Run(string(format), func(t *testing.T) {
			assertDocumentRejectedBeforeIO(t, model.DocumentPart{Name: "private-name", Format: format, Bytes: documentPDF}, model.ConversationRoleUser, "document format")
		})
	}
}

func TestOpenAIDocumentPreservesValidatedRequestBudget(t *testing.T) {
	transport := &documentTransport{}
	client, err := model.NewClient(newDocumentProvider(t, transport))
	require.NoError(t, err)
	req := documentRequest(model.DocumentPart{Name: "spec", Format: model.DocumentFormatPDF, Bytes: make([]byte, 16<<20)})
	response, err := client.Complete(t.Context(), req)
	assert.Nil(t, response)
	require.ErrorContains(t, err, "maximum size")
	stream, err := client.Stream(t.Context(), req)
	assert.Nil(t, stream)
	require.ErrorContains(t, err, "maximum size")
	assert.Zero(t, transport.calls)
}

func TestOpenAIDocumentDoesNotEnableToolResultParts(t *testing.T) {
	transport := &documentTransport{}
	provider := newDocumentProvider(t, transport)
	_, advertised := any(provider).(model.ToolResultPartsInfo)
	assert.False(t, advertised)
	client, err := model.NewClient(provider)
	require.NoError(t, err)
	_, advertised = client.(model.ToolResultPartsInfo)
	assert.False(t, advertised)
	req := &model.Request{Messages: []*model.Message{
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read", Input: map[string]any{}}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: "result", Parts: []model.ToolResultContentPart{model.DocumentPart{Name: "spec", Format: model.DocumentFormatPDF, Bytes: documentPDF}}}}},
	}}
	response, err := client.Complete(t.Context(), req)
	assert.Nil(t, response)
	require.ErrorIs(t, err, model.ErrUnsupportedPart)
	stream, err := client.Stream(t.Context(), req)
	assert.Nil(t, stream)
	require.ErrorIs(t, err, model.ErrUnsupportedPart)
	assert.Zero(t, transport.calls)
}

func TestOpenAIDocumentByteLimitsBeforeIO(t *testing.T) {
	bytes := make([]byte, 50_000_000)
	for _, tt := range []struct {
		name  string
		sizes []int
	}{
		{"file at limit", []int{50_000_000}},
		{"aggregate over limit", []int{25_000_000, 25_000_001}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			transport := &documentTransport{}
			provider := newDocumentProvider(t, transport)
			req := &model.Request{}
			for _, size := range tt.sizes {
				req.Messages = append(req.Messages, documentRequest(model.DocumentPart{Name: "spec", Format: model.DocumentFormatPDF, Bytes: bytes[:size]}).Messages...)
			}
			response, err := provider.Complete(t.Context(), req)
			assert.Nil(t, response)
			require.ErrorContains(t, err, "50000000")
			stream, err := provider.Stream(t.Context(), req)
			assert.Nil(t, stream)
			require.ErrorContains(t, err, "50000000")
			assert.Zero(t, transport.calls)
		})
	}
}

func assertDocumentRejectedBeforeIO(t *testing.T, part model.DocumentPart, role model.ConversationRole, want string) {
	t.Helper()
	transport := &documentTransport{}
	provider := newDocumentProvider(t, transport)
	req := documentRequest(part)
	req.Messages[0].Role = role
	response, err := provider.Complete(t.Context(), req)
	assert.Nil(t, response)
	require.ErrorContains(t, err, want)
	assert.NotContains(t, err.Error(), "private-")
	stream, err := provider.Stream(t.Context(), req)
	assert.Nil(t, stream)
	require.ErrorContains(t, err, want)
	assert.NotContains(t, err.Error(), "private-")
	assert.Zero(t, transport.calls)
}
