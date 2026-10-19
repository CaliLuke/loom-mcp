package gemini

import (
	"errors"
	"math"

	"google.golang.org/genai"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func addContinuationUsage(current, next model.TokenUsage) (model.TokenUsage, error) {
	knownTotal := continuationTotalKnown(current) && continuationTotalKnown(next)
	counts := []*int{&current.InputTokens, &current.OutputTokens, &current.TotalTokens, &current.CacheReadTokens, &current.CacheWriteTokens}
	deltas := []int{next.InputTokens, next.OutputTokens, next.TotalTokens, next.CacheReadTokens, next.CacheWriteTokens}
	for i, delta := range deltas {
		if delta < 0 || delta > math.MaxInt-*counts[i] {
			return model.TokenUsage{}, errors.New("continuation usage overflows")
		}
		*counts[i] += delta
	}
	if !knownTotal {
		current.TotalTokens = 0
	}
	if err := model.ValidateTokenUsage(current); err != nil {
		return model.TokenUsage{}, err
	}
	return current, nil
}

func continuationTotalKnown(usage model.TokenUsage) bool {
	return usage.TotalTokens != 0 || (usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CacheReadTokens == 0 && usage.CacheWriteTokens == 0)
}

// normalizeContinuationUsage validates primitive counts before widening arithmetic.
// Tool-execution prompts are billed input, but are not the original request size.
func normalizeContinuationUsage(modelID string, class model.ModelClass, raw *genai.GenerateContentResponseUsageMetadata) (model.TokenUsage, error) {
	counts := []int32{raw.PromptTokenCount, raw.ToolUsePromptTokenCount, raw.CachedContentTokenCount, raw.CandidatesTokenCount, raw.ThoughtsTokenCount, raw.TotalTokenCount}
	for _, count := range counts {
		if count < 0 {
			return model.TokenUsage{}, errors.New("continuation usage has a negative count")
		}
	}
	if raw.CachedContentTokenCount > raw.PromptTokenCount {
		return model.TokenUsage{}, errors.New("continuation cached input exceeds prompt")
	}
	input := int64(raw.PromptTokenCount) + int64(raw.ToolUsePromptTokenCount) - int64(raw.CachedContentTokenCount)
	output := int64(raw.CandidatesTokenCount) + int64(raw.ThoughtsTokenCount)
	if input > math.MaxInt || output > math.MaxInt {
		return model.TokenUsage{}, errors.New("continuation usage overflows")
	}
	usage := model.TokenUsage{Model: modelID, ModelClass: class, InputTokens: int(input), OutputTokens: int(output), CacheReadTokens: int(raw.CachedContentTokenCount), TotalTokens: int(raw.TotalTokenCount)}
	if err := model.ValidateTokenUsage(usage); err != nil {
		return model.TokenUsage{}, err
	}
	return usage, nil
}

// observeUsage bills each completed hop once, preserving the original input
// measurement separately. Missing or malformed later usage cannot erase it.
func (s *continuationGeneration) observeUsage(modelID string, class model.ModelClass, response *genai.GenerateContentResponse) error {
	if response == nil || response.UsageMetadata == nil {
		return errors.New("continuation requires hop usage")
	}
	hop, err := normalizeContinuationUsage(modelID, class, response.UsageMetadata)
	if err != nil {
		return err
	}
	if !s.hasUsage {
		s.response.Usage = hop
		s.response.RequestInputUsage.InputTokens = int(response.UsageMetadata.PromptTokenCount)
		s.hasUsage = true
		return nil
	}
	usage, err := addContinuationUsage(s.response.Usage, hop)
	if err != nil {
		return err
	}
	s.response.Usage = usage
	return nil
}
