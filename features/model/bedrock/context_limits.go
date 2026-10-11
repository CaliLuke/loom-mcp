package bedrock

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// No model-ID constants or literal model IDs exist in this adapter's non-test
// code or SDK. Keep the table empty under K10 decision 5; callers override it.
var contextLimitsTable = map[string]model.ContextLimits{}

// ContextLimits resolves the request model and reports table limits, if known.
func (c *Client) ContextLimits(_ context.Context, req *model.Request) (model.ContextLimits, string, bool) {
	id := c.resolveModelID(req)
	limits, known := contextLimitsTable[id]
	limits.DefaultMaxTokens = max(0, c.maxTok)
	return limits, id, known
}
