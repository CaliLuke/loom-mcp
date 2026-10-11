package gemini

import (
	"context"
	"strings"

	"github.com/CaliLuke/loom-mcp/v2/features/model/internal/contextlimits"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// No model-ID constants exist in genai and this adapter has no non-test IDs.
// Under K10 decision 5 the fallback stays empty; Models.Get can discover any ID.
var contextLimitsTable = map[string]model.ContextLimits{}

// ContextLimits discovers and caches raw model metadata in activities. Models
// API input limits become input + this request's OutputReserve after lookup,
// preserving the published input allowance when the reserve is subtracted.
func (c *Client) ContextLimits(ctx context.Context, req *model.Request) (model.ContextLimits, string, bool) {
	id := c.resolveModelID(req)
	limits, known := contextLimitsTable[id]
	limits.DefaultMaxTokens = max(0, c.maxTokens)
	fallback := contextlimits.Result{Limits: limits, Model: id, Known: known}
	models, ok := c.models.(contextInfoModelsClient)
	if !ok {
		return limits, id, known
	}
	result := c.limitsCache.Lookup(ctx, id, fallback, func(ctx context.Context) (contextlimits.Result, bool) {
		info, err := models.Get(ctx, id, nil)
		if err != nil || info == nil || info.InputTokenLimit <= 0 || info.OutputTokenLimit <= 0 {
			return fallback, false
		}
		resolved := strings.TrimPrefix(info.Name, "models/")
		if resolved == "" {
			resolved = id
		}
		return contextlimits.Result{
			Limits:     model.ContextLimits{MaxOutputTokens: int(info.OutputTokenLimit), DefaultMaxTokens: limits.DefaultMaxTokens},
			InputLimit: int(info.InputTokenLimit), Model: resolved, Known: true,
		}, true
	})
	resolvedLimits, resolvedKnown := result.ForRequest(req)
	return resolvedLimits, result.Model, resolvedKnown
}
