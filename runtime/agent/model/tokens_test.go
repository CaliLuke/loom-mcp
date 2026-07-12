package model

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenEstimatorCountsLargestToolProjection(t *testing.T) {
	estimator := TokenEstimator{CharactersPerToken: 1, OverheadTokens: 1}
	req := &Request{
		Model: "gemini-test",
		Messages: []*Message{
			{Role: ConversationRoleSystem, Parts: []Part{TextPart{Text: "system"}}},
			{Role: ConversationRoleUser, Parts: []Part{TextPart{Text: "hello"}}},
		},
		Tools: []*ToolDefinition{
			{
				Name:        "short",
				Description: "short",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"x": map[string]any{"type": "string"},
					},
				},
			},
			{
				Name:        "long",
				Description: "long",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"very_long_property_name": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	count, err := estimator.CountTokens(context.Background(), req)
	require.NoError(t, err)

	assert.False(t, count.Exact)
	assert.Equal(t, "gemini-test", count.Model)
	assert.Positive(t, count.InputTokens)
}

func TestTokenEstimatorCountsStructuredOutputSchema(t *testing.T) {
	estimator := TokenEstimator{CharactersPerToken: 1, OverheadTokens: 0, MinimumTokens: 1}
	base := &Request{
		Messages: []*Message{
			{Role: ConversationRoleUser, Parts: []Part{TextPart{Text: "hello"}}},
		},
	}
	withSchema := &Request{
		Messages: base.Messages,
		StructuredOutput: &StructuredOutput{
			Name:   "draft",
			Schema: []byte(`{"type":"object","required":["title"],"properties":{"title":{"type":"string"}}}`),
		},
	}

	baseCount, err := estimator.CountTokens(context.Background(), base)
	require.NoError(t, err)
	schemaCount, err := estimator.CountTokens(context.Background(), withSchema)
	require.NoError(t, err)

	assert.Greater(t, schemaCount.InputTokens, baseCount.InputTokens)
}

func TestTokenEstimatorCountsPointerAndValuePartsEqually(t *testing.T) {
	valueParts := []Part{
		TextPart{Text: "hello"},
		ImagePart{Format: ImageFormatPNG, Bytes: []byte{1, 2}},
		DocumentPart{Name: "doc", Format: DocumentFormatTXT, Text: "contents", Context: "reference"},
		CitationsPart{Text: "cited answer"},
		ThinkingPart{Text: "excluded reasoning"},
		ToolUsePart{ID: "call-1", Name: "search", Input: map[string]any{"q": "loom"}},
		ToolResultPart{ToolUseID: "call-1", Content: map[string]any{"found": true}},
		CacheCheckpointPart{},
	}
	pointerParts := []Part{
		&TextPart{Text: "hello"},
		&ImagePart{Format: ImageFormatPNG, Bytes: []byte{1, 2}},
		&DocumentPart{Name: "doc", Format: DocumentFormatTXT, Text: "contents", Context: "reference"},
		&CitationsPart{Text: "cited answer"},
		&ThinkingPart{Text: "excluded reasoning"},
		&ToolUsePart{ID: "call-1", Name: "search", Input: map[string]any{"q": "loom"}},
		&ToolResultPart{ToolUseID: "call-1", Content: map[string]any{"found": true}},
		&CacheCheckpointPart{},
	}
	estimator := TokenEstimator{CharactersPerToken: 1, OverheadTokens: 1}

	values, err := estimator.CountTokens(context.Background(), &Request{Messages: []*Message{{Role: ConversationRoleUser, Parts: valueParts}}})
	require.NoError(t, err)
	pointers, err := estimator.CountTokens(context.Background(), &Request{Messages: []*Message{{Role: ConversationRoleUser, Parts: pointerParts}}})
	require.NoError(t, err)

	assert.Equal(t, values, pointers)
}

func TestTokenEstimatorMinimumAndCancellation(t *testing.T) {
	count, err := (TokenEstimator{MinimumTokens: 17}).CountTokens(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, 17, count.InputTokens)

	count, err = (TokenEstimator{}).CountTokens(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, 500, count.InputTokens)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = (TokenEstimator{}).CountTokens(ctx, nil)
	assert.ErrorIs(t, err, context.Canceled)
}
