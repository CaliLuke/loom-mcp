package summarize

import (
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextChunkOverrides(t *testing.T) {
	evidence := Evidence{Units: []Unit{{MessageID: 1, Text: strings.Repeat("evidence ", 20000)}}}
	plan, err := PlanChunks(evidence, ChunkOptions{Limits: model.ContextLimits{ContextWindow: 8000}, MaxSummaryTokens: 1000, MaxSummaryChunks: 2})
	require.NoError(t, err)
	assert.Len(t, plan.Chunks, 2)
	assert.NotEmpty(t, plan.Unsummarised)
	evidence.Units[0].IsResult = true
	plan, err = PlanChunks(evidence, ChunkOptions{Limits: model.ContextLimits{ContextWindow: 8000}, MaxSummaryTokens: 1000, MaxSummarisedResultTokens: 20})
	require.NoError(t, err)
	require.Len(t, plan.Chunks, 1)
	assert.Equal(t, 60, plan.Chunks[0].Pieces[0].End)
}
