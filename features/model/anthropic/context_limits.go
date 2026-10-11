package anthropic

import (
	"context"
	"math"

	"github.com/CaliLuke/loom-mcp/v2/features/model/internal/contextlimits"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ModelsClient is the optional Anthropic Models API used to resolve aliases and
// discover input/output limits. It is satisfied by *sdk.ModelService.
type ModelsClient interface {
	// Get retrieves model metadata or resolves an alias.
	Get(ctx context.Context, modelID string, query sdk.ModelGetParams, opts ...option.RequestOption) (*sdk.ModelInfo, error)
}

var contextLimitsTable = map[string]model.ContextLimits{
	// Every context/output figure below: https://platform.claude.com/docs/en/models/overview
	// (1M context, 128K max output; verified 2026-10-10). These are shared windows,
	// so the table does not add a reserve as the input-only Models API does.
	sdk.ModelClaudeOpus5_5:   {ContextWindow: 1000000, MaxOutputTokens: 128000},
	sdk.ModelClaudeSonnet5_5: {ContextWindow: 1000000, MaxOutputTokens: 128000},
	sdk.ModelClaudeHaiku5_5:  {ContextWindow: 1000000, MaxOutputTokens: 128000},
	sdk.ModelClaudeFable5_1:  {ContextWindow: 1000000, MaxOutputTokens: 128000},
}

// ContextLimits discovers and caches raw model metadata in activities. Models
// API input limits become input + this request's OutputReserve after lookup,
// preserving the published input allowance when the reserve is subtracted.
func (c *Client) ContextLimits(ctx context.Context, req *model.Request) (model.ContextLimits, string, bool) {
	id := c.resolveModelID(req)
	limits, known := contextLimitsTable[id]
	limits.DefaultMaxTokens = max(0, c.maxTok)
	fallback := contextlimits.Result{Limits: limits, Model: id, Known: known}
	if c.models == nil {
		return limits, id, known
	}
	result := c.limitsCache.Lookup(ctx, id, fallback, func(ctx context.Context) (contextlimits.Result, bool) {
		info, err := c.models.Get(ctx, id, sdk.ModelGetParams{})
		if err != nil || info == nil || info.MaxInputTokens <= 0 || info.MaxTokens <= 0 || info.MaxInputTokens > int64(math.MaxInt) || info.MaxTokens > int64(math.MaxInt) {
			return fallback, false
		}
		resolved := info.ID
		if resolved == "" {
			resolved = id
		}
		return contextlimits.Result{
			Limits:     model.ContextLimits{MaxOutputTokens: int(info.MaxTokens), DefaultMaxTokens: limits.DefaultMaxTokens},
			InputLimit: int(info.MaxInputTokens), Model: resolved, Known: true,
		}, true
	})
	resolvedLimits, resolvedKnown := result.ForRequest(req)
	return resolvedLimits, result.Model, resolvedKnown
}
