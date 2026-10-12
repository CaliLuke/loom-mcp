package anthropic

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	sdk "github.com/anthropics/anthropic-sdk-go"
)

// ToolResultParts reports native image and PDF tool-result support for every model.
func (c *Client) ToolResultParts(context.Context, *model.Request) model.ToolResultPartSupport {
	return model.ToolResultPartSupport{
		ImageFormats: []model.ImageFormat{"png", "jpeg", "gif", "webp"}, DocumentFormats: []model.DocumentFormat{"pdf"},
		MaxImageBytes: 5_000_000, MaxDocumentBytes: 24_000_000,
	}
}

// anthropicDocumentBlock maps the two supported user-document sources.
func anthropicDocumentBlock(role model.ConversationRole, part model.DocumentPart) (sdk.ContentBlockParamUnion, error) {
	if role != model.ConversationRoleUser {
		return sdk.ContentBlockParamUnion{}, fmt.Errorf("anthropic: documents require user messages")
	}
	doc := sdk.DocumentBlockParam{Title: sdk.String(part.Name)}
	if part.Context != "" {
		doc.Context = sdk.String(part.Context)
	}
	if part.Cite {
		doc.Citations = sdk.CitationsConfigParam{Enabled: sdk.Bool(true)}
	}
	switch {
	case part.Text != "":
		doc.Source = sdk.DocumentBlockParamSourceUnion{OfText: &sdk.PlainTextSourceParam{Data: part.Text}}
	case part.Format == "pdf" && len(part.Bytes) > 0:
		doc.Source = sdk.DocumentBlockParamSourceUnion{OfBase64: &sdk.Base64PDFSourceParam{Data: base64.StdEncoding.EncodeToString(part.Bytes)}}
	default:
		return sdk.ContentBlockParamUnion{}, fmt.Errorf("anthropic: document requires PDF bytes or plain text")
	}
	return sdk.ContentBlockParamUnion{OfDocument: &doc}, nil
}

// appendToolResultParts preserves attachment order after request preflight validation.
func appendToolResultParts(block sdk.ContentBlockParamUnion, parts []model.ToolResultContentPart) (sdk.ContentBlockParamUnion, error) {
	for _, part := range parts {
		switch p := part.(type) {
		case *model.ImagePart:
			part = *p
		case *model.DocumentPart:
			part = *p
		}
		switch p := part.(type) {
		case model.ImagePart:
			image, err := anthropicImageBlock(model.ConversationRoleUser, p)
			if err != nil {
				return sdk.ContentBlockParamUnion{}, err
			}
			block.OfToolResult.Content = append(block.OfToolResult.Content, sdk.ToolResultBlockParamContentUnion{OfImage: image.OfImage})
		case model.DocumentPart:
			document, err := anthropicDocumentBlock(model.ConversationRoleUser, p)
			if err != nil {
				return sdk.ContentBlockParamUnion{}, err
			}
			block.OfToolResult.Content = append(block.OfToolResult.Content, sdk.ToolResultBlockParamContentUnion{OfDocument: document.OfDocument})
		}
	}
	return block, nil
}
