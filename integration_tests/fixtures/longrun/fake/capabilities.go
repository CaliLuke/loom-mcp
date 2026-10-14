package fake

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// ToolResultParts declares the formats decoded from actual request bytes.
func (r *Reader) ToolResultParts(context.Context, *model.Request) model.ToolResultPartSupport {
	if r.Profile != nil {
		return r.Profile.Support.Intersect(r.Profile.Support)
	}
	return attachmentSupport()
}

// ToolResultParts keeps summary-model support in the observed intersection.
func (s *Summariser) ToolResultParts(context.Context, *model.Request) model.ToolResultPartSupport {
	if s.Profile != nil {
		return s.Profile.Support.Intersect(s.Profile.Support)
	}
	return attachmentSupport()
}

func attachmentSupport() model.ToolResultPartSupport {
	return model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"png"}, DocumentFormats: []model.DocumentFormat{"pdf"}, MaxImageBytes: 1 << 20, MaxDocumentBytes: 1 << 20}
}
