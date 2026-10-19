package openai

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// Exercise admission separately so accepted boundary cases do not allocate
// large base64 envelopes; public request tests prove the pre-I/O wiring.
func TestDocumentInputByteAdmission(t *testing.T) {
	bytes := make([]byte, 50_000_000)
	for _, tt := range []struct {
		name    string
		sizes   []int
		wantErr string
	}{
		{"file below limit", []int{49_999_999}, ""},
		{"file at limit", []int{50_000_000}, "document must be smaller"},
		{"aggregate at limit", []int{25_000_000, 25_000_000}, ""},
		{"aggregate over limit", []int{25_000_000, 25_000_001}, "documents exceed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			messages := []*model.Message{nil, {Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "read"}}}}
			for _, size := range tt.sizes {
				messages = append(messages, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.DocumentPart{Name: "spec", Format: model.DocumentFormatPDF, Bytes: bytes[:size]}}})
			}
			err := validateDocumentInputs(messages)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
