package bedrock

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBedrockRequestFailuresBeforeProviderIO(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*model.Request)
		want string
	}{
		{"no messages", func(r *model.Request) {
			r.Messages = nil
		}, "messages are required"},
		{"nova tool cache", func(r *model.Request) {
			r.Model = "amazon.nova-pro-v1:0"
			r.Cache = &model.CacheOptions{AfterTools: true}
		}, "not supported for Nova"},
		{"tool choice without tools", func(r *model.Request) {
			r.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeAny}
		}, "no tools"},
		{"tool history without catalog", func(r *model.Request) {
			r.Messages = append(r.Messages, &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "lookup", Input: map[string]any{}}}})
		}, "no tools provided"},
		{"missing output schema", func(r *model.Request) {
			r.StructuredOutput = &model.StructuredOutput{Name: "result"}
		}, "schema"},
		{"malformed output schema", func(r *model.Request) {
			r.StructuredOutput = &model.StructuredOutput{Name: "result", Schema: rawjson.Message(`{`)}
		}, "schema"},
		{"system only", func(r *model.Request) {
			r.Messages[0].Role = model.ConversationRoleSystem
		}, "at least one"},
		{"all tools missing", func(r *model.Request) {
			r.Tools = []*model.ToolDefinition{nil}
			r.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeAny}
		}, "no tools"},
		{"missing chosen tool", func(r *model.Request) {
			r.Tools = []*model.ToolDefinition{{Name: "lookup", Description: "Lookup", InputSchema: map[string]any{"type": "object"}}}
			r.ToolChoice = &model.ToolChoice{Mode: model.ToolChoiceModeTool, Name: "other"}
		}, "does not match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &errorRuntimeClient{}
			client := &Client{runtime: provider, defaultModel: "anthropic.claude-3-5-sonnet-20240620-v1:0", logger: telemetry.NewNoopLogger()}
			request := &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}}
			tc.edit(request)
			_, err := client.Complete(t.Context(), request)
			require.ErrorContains(t, err, tc.want)
			stream, err := client.Stream(t.Context(), request)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, stream)
			assert.Nil(t, provider.converseInput)
			assert.Nil(t, provider.converseStreamInput)
		})
	}
}

func TestBedrockCountTokensFailures(t *testing.T) {
	client := &Client{runtime: &errorRuntimeClient{}, defaultModel: "claude", logger: telemetry.NewNoopLogger()}
	request := &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}}
	_, err := client.CountTokens(t.Context(), request)
	require.ErrorContains(t, err, "does not support count_tokens")
	provider := &countTokensRuntimeClient{err: assert.AnError}
	client = &Client{runtime: provider, defaultModel: "claude", logger: telemetry.NewNoopLogger()}
	_, err = client.CountTokens(t.Context(), &model.Request{})
	require.ErrorContains(t, err, "messages are required")
	assert.Nil(t, provider.input)
	_, err = client.CountTokens(t.Context(), request)
	require.ErrorIs(t, err, assert.AnError)
}
