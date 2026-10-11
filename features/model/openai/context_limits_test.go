package openai_test

import (
	"context"
	"testing"

	openaimodel "github.com/CaliLuke/loom-mcp/v2/features/model/openai"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextLimitsTable(t *testing.T) {
	client, err := openaimodel.New(openaimodel.Options{Client: &mockResponsesClient{}, DefaultModel: "gpt-4o", HighModel: "gpt-4.1", SmallModel: "gpt-4o-mini"})
	require.NoError(t, err)
	info, ok := any(client).(model.ContextInfo)
	require.True(t, ok)
	for _, tc := range []struct {
		id             string
		window, output int
	}{
		{"gpt-4o", 128000, 16384}, {"gpt-4o-mini", 128000, 16384},
		{"gpt-4.1", 1047576, 32768}, {"gpt-4.1-mini", 1047576, 32768},
		{"unknown", 0, 0},
	} {
		t.Run(tc.id, func(t *testing.T) {
			limits, resolved, known := info.ContextLimits(context.Background(), &model.Request{Model: tc.id})
			assert.Equal(t, tc.id, resolved)
			assert.Equal(t, tc.window > 0, known)
			assert.Equal(t, model.ContextLimits{ContextWindow: tc.window, MaxOutputTokens: tc.output}, limits)
		})
	}
	for _, tc := range []struct {
		class model.ModelClass
		id    string
	}{
		{model.ModelClassDefault, "gpt-4o"}, {model.ModelClassHighReasoning, "gpt-4.1"}, {model.ModelClassSmall, "gpt-4o-mini"},
	} {
		_, resolved, known := info.ContextLimits(context.Background(), &model.Request{ModelClass: tc.class})
		assert.True(t, known)
		assert.Equal(t, tc.id, resolved)
	}
}

func TestContextLimitsDeprecatedModelUnknown(t *testing.T) {
	client, err := openaimodel.New(openaimodel.Options{Client: &mockResponsesClient{}, DefaultModel: "gpt-4.1-nano"})
	require.NoError(t, err)
	info, ok := any(client).(model.ContextInfo)
	require.True(t, ok)
	limits, resolved, known := info.ContextLimits(t.Context(), &model.Request{})
	assert.False(t, known)
	assert.Equal(t, "gpt-4.1-nano", resolved)
	assert.Zero(t, limits)
}
