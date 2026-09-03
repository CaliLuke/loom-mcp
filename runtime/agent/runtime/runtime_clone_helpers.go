package runtime

import (
	"errors"
	"fmt"
	"maps"
	"math"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/artifact"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
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

func cloneToolIdents(src []tools.Ident) []tools.Ident {
	if len(src) == 0 {
		return nil
	}
	dst := make([]tools.Ident, len(src))
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
		cp.Artifacts = cloneArtifactContents(tr.Artifacts)
		out = append(out, &cp)
	}
	return out
}

func artifactRefsFromContents(contents []artifact.Content) []artifact.Ref {
	if len(contents) == 0 {
		return nil
	}
	refs := make([]artifact.Ref, 0, len(contents))
	for _, content := range contents {
		if content.Ref.ID == "" && content.Ref.Name == "" && content.Ref.MimeType == "" && content.Ref.SizeBytes == 0 {
			continue
		}
		refs = append(refs, cloneArtifactRef(content.Ref))
	}
	return refs
}

func artifactContentsFromRefs(refs []artifact.Ref) []artifact.Content {
	if len(refs) == 0 {
		return nil
	}
	contents := make([]artifact.Content, len(refs))
	for i, ref := range refs {
		contents[i] = artifact.Content{Ref: cloneArtifactRef(ref)}
	}
	return contents
}

func cloneArtifactContents(contents []artifact.Content) []artifact.Content {
	if len(contents) == 0 {
		return nil
	}
	cloned := make([]artifact.Content, len(contents))
	for i, content := range contents {
		cloned[i] = content
		cloned[i].Ref = cloneArtifactRef(content.Ref)
		cloned[i].Body = append([]byte(nil), content.Body...)
	}
	return cloned
}

func cloneArtifactRefs(refs []artifact.Ref) []artifact.Ref {
	if len(refs) == 0 {
		return nil
	}
	cloned := make([]artifact.Ref, len(refs))
	for i, ref := range refs {
		cloned[i] = cloneArtifactRef(ref)
	}
	return cloned
}

func cloneArtifactRef(ref artifact.Ref) artifact.Ref {
	ref.Metadata = maps.Clone(ref.Metadata)
	return ref
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

func tokenUsageTotalKnown(usage model.TokenUsage) bool {
	if usage.TotalTokens != 0 {
		return true
	}
	return usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CacheReadTokens == 0 && usage.CacheWriteTokens == 0
}

func checkedAddTokenUsage(current, delta model.TokenUsage) (model.TokenUsage, error) {
	if err := model.ValidateTokenUsage(current); err != nil {
		return model.TokenUsage{}, fmt.Errorf("current usage: %w", err)
	}
	if err := model.ValidateTokenUsage(delta); err != nil {
		return model.TokenUsage{}, fmt.Errorf("usage delta: %w", err)
	}
	counts := [][2]int{
		{current.InputTokens, delta.InputTokens},
		{current.OutputTokens, delta.OutputTokens},
		{current.TotalTokens, delta.TotalTokens},
		{current.CacheReadTokens, delta.CacheReadTokens},
		{current.CacheWriteTokens, delta.CacheWriteTokens},
	}
	for _, count := range counts {
		if count[0] > math.MaxInt-count[1] {
			return model.TokenUsage{}, errors.New("token usage aggregation overflows")
		}
	}
	usage := addTokenUsage(current, delta)
	currentTotalKnown := tokenUsageTotalKnown(current)
	deltaTotalKnown := tokenUsageTotalKnown(delta)
	if !currentTotalKnown || !deltaTotalKnown {
		usage.TotalTokens = 0
	}
	if err := model.ValidateTokenUsage(usage); err != nil {
		return model.TokenUsage{}, fmt.Errorf("aggregated usage: %w", err)
	}
	return usage, nil
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
