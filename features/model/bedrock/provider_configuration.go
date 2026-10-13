package bedrock

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/features/model/internal/claudecaps"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// ProviderConfiguration identifies the provider route used for this request.
func (c *Client) ProviderConfiguration(_ context.Context, req *model.Request) string {
	if claudecaps.IsClaude(c.resolveModelID(req)) {
		return "bedrock-anthropic"
	}
	return "bedrock"
}
