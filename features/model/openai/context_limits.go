package openai

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/openai/openai-go/v3/shared"
)

var contextLimitsTable = map[string]model.ContextLimits{
	// Both figures: https://developers.openai.com/api/docs/models/gpt-4o (verified 2026-10-10).
	shared.ChatModelGPT4o: {ContextWindow: 128000, MaxOutputTokens: 16384},
	// Both figures: https://developers.openai.com/api/docs/models/gpt-4o-mini (verified 2026-10-10).
	shared.ChatModelGPT4oMini: {ContextWindow: 128000, MaxOutputTokens: 16384},
	// Both figures: https://developers.openai.com/api/docs/models/gpt-4.1 (verified 2026-10-10).
	shared.ChatModelGPT4_1: {ContextWindow: 1047576, MaxOutputTokens: 32768},
	// Both figures: https://developers.openai.com/api/docs/models/gpt-4.1-mini (verified 2026-10-10).
	shared.ChatModelGPT4_1Mini: {ContextWindow: 1047576, MaxOutputTokens: 32768},
}

// ContextLimits resolves the same model as Complete and returns verified table
// limits. Unlisted models remain unknown until a runtime override is supplied.
func (c *Client) ContextLimits(_ context.Context, req *model.Request) (model.ContextLimits, string, bool) {
	id := c.resolveModelID(req)
	limits, known := contextLimitsTable[id]
	return limits, id, known
}
