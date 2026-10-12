package fake

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// ToolResultParts declares the formats decoded from actual request bytes.
func (*Reader) ToolResultParts(context.Context, *model.Request) model.ToolResultPartSupport {
	return attachmentSupport()
}

// ToolResultParts keeps summary-model support in the observed intersection.
func (*Summariser) ToolResultParts(context.Context, *model.Request) model.ToolResultPartSupport {
	return attachmentSupport()
}

func attachmentSupport() model.ToolResultPartSupport {
	return model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"png"}, DocumentFormats: []model.DocumentFormat{"pdf"}, MaxImageBytes: 1 << 20, MaxDocumentBytes: 1 << 20}
}
