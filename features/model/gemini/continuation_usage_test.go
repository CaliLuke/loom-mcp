package gemini

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestContinuationUsageAddition(t *testing.T) {
	usage, err := addContinuationUsage(model.TokenUsage{Model: "model", InputTokens: 5, CacheReadTokens: 2}, model.TokenUsage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10})
	require.NoError(t, err)
	assert.Equal(t, model.TokenUsage{Model: "model", InputTokens: 12, CacheReadTokens: 2, OutputTokens: 3}, usage)
	_, err = addContinuationUsage(model.TokenUsage{InputTokens: math.MaxInt}, model.TokenUsage{CacheReadTokens: 1})
	require.Error(t, err)
	_, err = addContinuationUsage(model.TokenUsage{InputTokens: math.MaxInt}, model.TokenUsage{InputTokens: 1})
	require.Error(t, err)
}

func TestContinuationRawUsageValidation(t *testing.T) {
	for _, usage := range []*genai.GenerateContentResponseUsageMetadata{
		{PromptTokenCount: -1, ToolUsePromptTokenCount: 1},
		{CandidatesTokenCount: -1, ThoughtsTokenCount: 1},
		{PromptTokenCount: 1, CachedContentTokenCount: 2, ToolUsePromptTokenCount: 2},
		{CachedContentTokenCount: -1},
		{ToolUsePromptTokenCount: -1},
		{ThoughtsTokenCount: -1},
		{TotalTokenCount: -1},
	} {
		state := continuationGeneration{}
		err := state.observeUsage("model", "", &genai.GenerateContentResponse{UsageMetadata: usage})
		require.Error(t, err)
		assert.False(t, state.hasUsage)
	}
	state := continuationGeneration{}
	err := state.observeUsage("model", "", &genai.GenerateContentResponse{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: math.MaxInt32, ToolUsePromptTokenCount: 1, CandidatesTokenCount: math.MaxInt32, ThoughtsTokenCount: 1}})
	require.NoError(t, err)
	assert.Equal(t, int64(math.MaxInt32)+1, int64(state.response.Usage.InputTokens))
	assert.Equal(t, int64(math.MaxInt32)+1, int64(state.response.Usage.OutputTokens))
	assert.Equal(t, int(math.MaxInt32), state.response.RequestInputUsage.InputTokens)
}
