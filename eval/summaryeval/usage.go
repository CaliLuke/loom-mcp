package summaryeval

import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"

func tokenCount(u model.TokenUsage) int {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}
