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
