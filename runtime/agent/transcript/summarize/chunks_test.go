package summarize

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChunkPlanningWindowsAndCoverage(t *testing.T) {
	for _, window := range []int{8000, 200000} {
		for _, maxSummary := range []int{400, 16000} {
			for _, text := range []string{strings.Repeat("界\n", 18000), strings.Repeat("x", 120000)} {
				evidence := Evidence{Previous: "previous [message 1]", Units: []Unit{{MessageID: 1, Part: 1, Label: "[message 1]", Text: text}}}
				options := ChunkOptions{Limits: model.ContextLimits{ContextWindow: window}, MaxSummaryTokens: maxSummary, Prompt: "summarise quoted data"}
				plan, err := PlanChunks(evidence, options)
				require.NoError(t, err)
				assert.Equal(t, min(maxSummary, plan.Budget/4), plan.OutputTokens)
				assert.Equal(t, window-plan.OutputTokens, plan.Budget)
				require.LessOrEqual(t, len(plan.Chunks), MaxSummaryChunks)
				require.NotEmpty(t, plan.Chunks)
				again, err := PlanChunks(evidence, options)
				require.NoError(t, err)
				assert.Equal(t, plan, again)
				offset := 0
				for _, chunk := range plan.Chunks {
					request := RenderChunk(options.Prompt, strings.Repeat("\n", plan.OutputTokens*3), chunk)
					facts, err := contextmeasure.Measure(request, options.Limits, nil)
					require.NoError(t, err)
					estimate := contextmeasure.Estimate(contextmeasure.Rate{}, 0, facts.TextBytes, facts.NonText)
					assert.LessOrEqual(t, estimate, plan.Budget-plan.Budget/10-boolInt(plan.Budget%10 != 0))
					assert.Equal(t, plan.OutputTokens, request.MaxTokens)
					for _, piece := range chunk.Pieces {
						assert.Equal(t, offset, piece.Start)
						assert.True(t, utf8.ValidString(piece.Unit.Text[piece.Start:piece.End]))
						offset = piece.End
					}
				}
				if offset < len(text) {
					assert.Equal(t, []ByteRange{{MessageID: 1, Part: 1, Start: offset, End: len(text)}}, plan.Unsummarised)
				} else {
					assert.Empty(t, plan.Unsummarised)
				}
			}
		}
	}
}

func TestResultAndChunkLimitsExactRanges(t *testing.T) {
	options := ChunkOptions{Limits: model.ContextLimits{ContextWindow: 8000}, MaxSummaryTokens: 1600, Prompt: strings.Repeat("p", 5000)}
	text := strings.Repeat("界", 10000)
	evidence := Evidence{Units: []Unit{{MessageID: 2, Part: 1, ToolUseID: "call-1", Label: "result call-1", Text: text, IsResult: true}}}
	plan, err := PlanChunks(evidence, options)
	require.NoError(t, err)
	expectedEnd := plan.Budget / 2 * 3
	expectedEnd -= expectedEnd % 3
	require.Greater(t, len(plan.Chunks), 1)
	assert.Equal(t, []ByteRange{{MessageID: 2, ToolUseID: "call-1", Part: 1, Start: expectedEnd, End: len(text), IsResult: true}}, plan.Unsummarised)
	offset := 0
	for _, chunk := range plan.Chunks {
		for _, piece := range chunk.Pieces {
			assert.Equal(t, offset, piece.Start)
			offset = piece.End
		}
	}
	assert.Equal(t, expectedEnd, offset)
	evidence.Units = nil
	for id := 1; id <= 20; id++ {
		evidence.Units = append(evidence.Units, Unit{MessageID: index.MessageID(id), Part: 1, ToolUseID: "call", Label: "result", Text: text, IsResult: true})
	}
	plan, err = PlanChunks(evidence, options)
	require.NoError(t, err)
	require.Len(t, plan.Chunks, MaxSummaryChunks)
	covered := make(map[index.MessageID]int)
	for _, chunk := range plan.Chunks {
		for _, piece := range chunk.Pieces {
			assert.Equal(t, covered[piece.Unit.MessageID], piece.Start)
			covered[piece.Unit.MessageID] = piece.End
		}
	}
	require.Len(t, plan.Unsummarised, len(evidence.Units))
	for i, gap := range plan.Unsummarised {
		assert.Equal(t, index.MessageID(i+1), gap.MessageID)
		assert.Equal(t, covered[gap.MessageID], gap.Start)
		assert.Equal(t, len(text), gap.End)
		assert.Equal(t, "call", gap.ToolUseID)
		assert.Equal(t, text[gap.Start:], evidence.Units[i].Text[gap.Start:gap.End])
	}
}

func TestChunkUnknownLimitsAndNonText(t *testing.T) {
	evidence := Evidence{Units: []Unit{{MessageID: 1, Part: 1, Label: "[message 1]", Attachment: model.ImagePart{Format: "png", Bytes: []byte("image")}}}}
	_, err := PlanChunks(evidence, ChunkOptions{MaxSummaryTokens: 100})
	var rejection *Rejection
	require.ErrorAs(t, err, &rejection)
	assert.Equal(t, "summariser_limits_unknown", rejection.Kind)
	for _, window := range []int{1000, 8000} {
		options := ChunkOptions{Limits: model.ContextLimits{ContextWindow: window}, MaxSummaryTokens: 100}
		plan, err := PlanChunks(evidence, options)
		require.NoError(t, err)
		if window == 1000 {
			assert.Empty(t, plan.Chunks)
			require.Len(t, plan.Annotations, 1)
			assert.Equal(t, "not summarised: image", plan.Annotations[0].String())
		} else {
			require.Len(t, plan.Chunks, 1)
			request := RenderChunk("", "", plan.Chunks[0])
			assert.IsType(t, model.ImagePart{}, request.Messages[1].Parts[1])
		}
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func TestChunkIntegerCapAndPromptAccounting(t *testing.T) {
	evidence := Evidence{Units: []Unit{{MessageID: 1, Part: 1, Label: "[message 1]", Text: "界"}}}
	for _, window := range []int{8000, 8001, 8002, 8003, 8004} {
		options := ChunkOptions{Limits: model.ContextLimits{ContextWindow: window}, MaxSummaryTokens: 16000}
		plan, err := PlanChunks(evidence, options)
		require.NoError(t, err)
		assert.Equal(t, window/5, plan.OutputTokens)
		assert.LessOrEqual(t, plan.OutputTokens, plan.Budget/4)
		assert.Equal(t, window-plan.OutputTokens, plan.Budget)
	}
	plan, err := PlanChunks(evidence, ChunkOptions{Limits: model.ContextLimits{ContextWindow: 8000}, MaxSummaryTokens: 100, Prompt: strings.Repeat("p", 30000)})
	require.NoError(t, err)
	assert.Empty(t, plan.Chunks)
	assert.Equal(t, []ByteRange{{MessageID: 1, Part: 1, Start: 0, End: len("界")}}, plan.Unsummarised)
}
