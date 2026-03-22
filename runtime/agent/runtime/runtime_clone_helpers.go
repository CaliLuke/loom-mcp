package runtime

import (
	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
)

// isZeroRetryPolicy checks if a retry policy is effectively zero.
func isZeroRetryPolicy(policy engine.RetryPolicy) bool {
	return policy.MaxAttempts == 0 && policy.InitialInterval == 0 && policy.BackoffCoefficient == 0
}

// cloneLabels creates a defensive copy of a string map.
func cloneLabels(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// cloneMetadata creates a defensive copy of an arbitrary metadata map.
func cloneMetadata(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// cloneStrings creates a defensive copy of a string slice.
func cloneStrings(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

// cloneToolResults creates a shallow copy of a tool result slice.
func cloneToolResults(src []*planner.ToolResult) []*planner.ToolResult {
	if len(src) == 0 {
		return nil
	}
	out := make([]*planner.ToolResult, 0, len(src))
	for _, tr := range src {
		if tr == nil {
			out = append(out, nil)
			continue
		}
		cp := *tr
		out = append(out, &cp)
	}
	return out
}

func addTokenUsage(current, delta model.TokenUsage) model.TokenUsage {
	return model.TokenUsage{
		InputTokens:      current.InputTokens + delta.InputTokens,
		OutputTokens:     current.OutputTokens + delta.OutputTokens,
		TotalTokens:      current.TotalTokens + delta.TotalTokens,
		CacheReadTokens:  current.CacheReadTokens + delta.CacheReadTokens,
		CacheWriteTokens: current.CacheWriteTokens + delta.CacheWriteTokens,
	}
}

// mergeLabels merges src labels into dst.
func mergeLabels(dst map[string]string, src map[string]string) map[string]string {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]string, len(src))
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
