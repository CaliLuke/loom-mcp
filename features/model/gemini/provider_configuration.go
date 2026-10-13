package gemini

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// ProviderConfiguration identifies the provider route used for this request.
func (c *Client) ProviderConfiguration(_ context.Context, req *model.Request) string {
	return "gemini"
}
