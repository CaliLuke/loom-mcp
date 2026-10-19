package openai

import (
	"encoding/base64"
	"errors"
	"fmt"
	"unicode/utf8"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// OpenAI requires each file to be under 50 MB, with at most 50 MB combined.
// Use decimal MB conservatively; validated clients retain their smaller
// canonical request budget. https://developers.openai.com/api/docs/guides/file-inputs
const maxDocumentInputBytes = 50_000_000

// validateDocumentInputs admits the entire request before allocating base64
// envelopes. It does not advertise support for nested tool-result attachments.
func validateDocumentInputs(messages []*model.Message) error {
	total := 0
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		for _, part := range msg.Parts {
			doc, ok := part.(model.DocumentPart)
			if !ok {
				continue
			}
			if err := validateDocumentInput(msg.Role, doc); err != nil {
				return err
			}
			if len(doc.Bytes) > maxDocumentInputBytes-total {
				return fmt.Errorf("openai responses: documents exceed %d combined bytes", maxDocumentInputBytes)
			}
			total += len(doc.Bytes)
		}
	}
	return nil
}

// validateDocumentInput enforces the supported user-message PDF byte subset
// without changing accepted names or silently discarding unsupported fields.
func validateDocumentInput(role model.ConversationRole, doc model.DocumentPart) error {
	switch {
	case role != model.ConversationRoleUser:
		return errors.New("openai responses: document parts are only supported in user messages")
	case doc.Format != model.DocumentFormatPDF:
		return errors.New("openai responses: unsupported document format; expected pdf")
	case doc.Name == "" || !utf8.ValidString(doc.Name):
		return errors.New("openai responses: document name must be nonempty valid UTF-8")
	case len(doc.Bytes) == 0:
		return errors.New("openai responses: document bytes are required")
	case doc.Text != "" || len(doc.Chunks) > 0 || doc.URI != "":
		return errors.New("openai responses: documents support only a byte source")
	case doc.Context != "":
		return errors.New("openai responses: document context is not supported")
	case doc.Cite:
		return errors.New("openai responses: document citations are not supported")
	case len(doc.Bytes) >= maxDocumentInputBytes:
		return fmt.Errorf("openai responses: each document must be smaller than %d bytes", maxDocumentInputBytes)
	default:
		return nil
	}
}

// encodeDocumentContent preserves a document admitted by validateDocumentInputs.
func encodeDocumentContent(doc model.DocumentPart) responses.ResponseInputContentUnionParam {
	return responses.ResponseInputContentUnionParam{
		OfInputFile: &responses.ResponseInputFileParam{
			Filename: openai.String(doc.Name),
			FileData: openai.String("data:application/pdf;base64," + base64.StdEncoding.EncodeToString(doc.Bytes)),
		},
	}
}
