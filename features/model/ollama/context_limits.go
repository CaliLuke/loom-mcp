package ollama

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// ContextLimits reports the configured completion cap, but leaves the effective
// num_ctx window unknown. Supply its deployment-specific value via an override.
func (c *Client) ContextLimits(_ context.Context, req *model.Request) (model.ContextLimits, string, bool) {
	return model.ContextLimits{DefaultMaxTokens: max(0, c.maxTokens)}, c.resolveModelID(req), false
}
