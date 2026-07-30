package bedrock

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
)

type countTokensRuntimeClient struct {
	input *bedrockruntime.CountTokensInput
	err   error
}

func (c *countTokensRuntimeClient) Converse(
	context.Context,
	*bedrockruntime.ConverseInput,
	...func(*bedrockruntime.Options),
) (*bedrockruntime.ConverseOutput, error) {
	return nil, nil
}

func (c *countTokensRuntimeClient) ConverseStream(
	context.Context,
	*bedrockruntime.ConverseStreamInput,
	...func(*bedrockruntime.Options),
) (*bedrockruntime.ConverseStreamOutput, error) {
	return nil, nil
}

func (c *countTokensRuntimeClient) CountTokens(
	_ context.Context,
	input *bedrockruntime.CountTokensInput,
	_ ...func(*bedrockruntime.Options),
) (*bedrockruntime.CountTokensOutput, error) {
	c.input = input
	tokens := int32(42)
	return &bedrockruntime.CountTokensOutput{InputTokens: &tokens}, c.err
}

func TestCountTokensUsesConverseRequestPreparation(t *testing.T) {
	rt := &countTokensRuntimeClient{}
	client := &Client{
		runtime:      rt,
		defaultModel: "test-model",
		maxTok:       10,
		temp:         0.5,
		think:        defaultThinkingBudget,
		logger:       telemetry.NewNoopLogger(),
	}
	req := &model.Request{
		ModelClass: model.ModelClassDefault,
		Messages: []*model.Message{
			{
				Role:  model.ConversationRoleSystem,
				Parts: []model.Part{model.TextPart{Text: "system prompt"}},
			},
			{
				Role:  model.ConversationRoleUser,
				Parts: []model.Part{model.TextPart{Text: "hello"}},
			},
		},
		Tools: []*model.ToolDefinition{
			{
				Name:        "lookup",
				Description: "Look up a value.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	count, err := client.CountTokens(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, 42, count.InputTokens)
	require.Equal(t, "test-model", count.Model)
	require.Equal(t, model.ModelClassDefault, count.ModelClass)
	require.True(t, count.Exact)

	require.NotNil(t, rt.input)
	require.Equal(t, "test-model", *rt.input.ModelId)
	converse, ok := rt.input.Input.(*brtypes.CountTokensInputMemberConverse)
	require.True(t, ok)
	require.Len(t, converse.Value.System, 1)
	require.Len(t, converse.Value.Messages, 1)
	require.NotNil(t, converse.Value.ToolConfig)
}

func TestCountTokensOmitsThinkingBlocks(t *testing.T) {
	rt := &countTokensRuntimeClient{}
	client := &Client{
		runtime:      rt,
		defaultModel: "test-model",
		maxTok:       10,
		temp:         0.5,
		think:        defaultThinkingBudget,
		logger:       telemetry.NewNoopLogger(),
	}
	req := &model.Request{
		ModelClass: model.ModelClassDefault,
		Messages: []*model.Message{
			{
				Role:  model.ConversationRoleUser,
				Parts: []model.Part{model.TextPart{Text: "hello"}},
			},
			{
				Role: model.ConversationRoleAssistant,
				Parts: []model.Part{
					model.ThinkingPart{Text: "reasoning", Signature: "sig", Final: true},
					model.ToolUsePart{ID: "call-1", Name: "lookup", Input: map[string]any{"id": "a"}},
				},
			},
			{
				Role: model.ConversationRoleAssistant,
				Parts: []model.Part{
					model.ThinkingPart{Text: "thinking only", Signature: "sig2", Final: true},
				},
			},
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.ToolResultPart{ToolUseID: "call-1", Content: "ok"},
				},
			},
		},
		Tools: []*model.ToolDefinition{
			{
				Name:        "lookup",
				Description: "Look up a value.",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	}

	count, err := client.CountTokens(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, 42, count.InputTokens)

	require.NotNil(t, rt.input)
	converse, ok := rt.input.Input.(*brtypes.CountTokensInputMemberConverse)
	require.True(t, ok)
	require.Len(t, converse.Value.Messages, 3)
	for _, msg := range converse.Value.Messages {
		for _, block := range msg.Content {
			_, isReasoning := block.(*brtypes.ContentBlockMemberReasoningContent)
			require.False(t, isReasoning, "count input must not contain reasoning content")
		}
	}
	require.Len(t, req.Messages, 4)
	require.Len(t, req.Messages[1].Parts, 2)
}
