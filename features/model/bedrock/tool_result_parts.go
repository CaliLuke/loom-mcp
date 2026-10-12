package bedrock

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/features/model/internal/claudecaps"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// ToolResultParts reports uploads and S3 documents for recognized Claude models.
func (c *Client) ToolResultParts(_ context.Context, req *model.Request) model.ToolResultPartSupport {
	if !claudecaps.IsClaude(c.resolveModelID(req)) {
		return model.ToolResultPartSupport{}
	}
	return model.ToolResultPartSupport{
		ImageFormats:       []model.ImageFormat{"png", "jpeg", "gif", "webp"},
		DocumentFormats:    []model.DocumentFormat{"pdf", "csv", "doc", "docx", "xls", "xlsx", "html", "txt", "md"},
		DocumentURISchemes: []string{"s3"}, MaxImageBytes: 3_750_000, MaxDocumentBytes: 4_500_000,
	}
}
