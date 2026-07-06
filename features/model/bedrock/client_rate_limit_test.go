package bedrock

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

type errorRuntimeClient struct {
	converseErr       error
	converseStreamErr error
	converseInput     *bedrockruntime.ConverseInput
}

func (e *errorRuntimeClient) Converse(
	_ context.Context,
	input *bedrockruntime.ConverseInput,
	_ ...func(*bedrockruntime.Options),
) (*bedrockruntime.ConverseOutput, error) {
	e.converseInput = input
	if e.converseErr != nil {
		return nil, e.converseErr
	}
	return &bedrockruntime.ConverseOutput{}, nil
}

func (e *errorRuntimeClient) ConverseStream(
	_ context.Context,
	_ *bedrockruntime.ConverseStreamInput,
	_ ...func(*bedrockruntime.Options),
) (*bedrockruntime.ConverseStreamOutput, error) {
	return nil, e.converseStreamErr
}

func TestIsRateLimited_IdempotentOnSentinel(t *testing.T) {
	err := model.ErrRateLimited
	require.True(t, isRateLimited(err))

	wrapped := fmt.Errorf("provider: %w", err)
	require.True(t, isRateLimited(wrapped))
}

func TestComplete_WrapsRateLimitedErrors(t *testing.T) {
	rt := &errorRuntimeClient{
		converseErr: model.ErrRateLimited,
	}
	client := &Client{
		runtime:      rt,
		defaultModel: "test-model",
		maxTok:       10,
		temp:         0.5,
		think:        defaultThinkingBudget,
	}
	req := model.Request{
		ModelClass: model.ModelClassDefault,
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: "hello"},
				},
			},
		},
	}
	_, err := client.Complete(context.Background(), &req)
	require.Error(t, err)
	require.ErrorIs(t, err, model.ErrRateLimited)
}

func TestStream_WrapsRateLimitedErrors(t *testing.T) {
	rt := &errorRuntimeClient{
		converseStreamErr: model.ErrRateLimited,
	}
	client := &Client{
		runtime:      rt,
		defaultModel: "test-model",
		maxTok:       10,
		temp:         0.5,
		think:        defaultThinkingBudget,
	}
	req := model.Request{
		ModelClass: model.ModelClassDefault,
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: "hello"},
				},
			},
		},
	}
	stream, err := client.Stream(context.Background(), &req)
	require.Error(t, err)
	require.ErrorIs(t, err, model.ErrRateLimited)
	require.Nil(t, stream)
}

func TestComplete_SetsStructuredOutputConfig(t *testing.T) {
	rt := &errorRuntimeClient{}
	client := &Client{
		runtime:      rt,
		defaultModel: "test-model",
		maxTok:       10,
		temp:         0.5,
		think:        defaultThinkingBudget,
	}
	req := model.Request{
		ModelClass: model.ModelClassDefault,
		Messages: []*model.Message{
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}},
		},
		StructuredOutput: &model.StructuredOutput{
			Name:   "draft",
			Schema: []byte(`{"type":"object"}`),
		},
	}

	resp, err := client.Complete(context.Background(), &req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, rt.converseInput)
	require.NotNil(t, rt.converseInput.OutputConfig)
	require.NotNil(t, rt.converseInput.OutputConfig.TextFormat)
}

func TestStream_RejectsStructuredOutput(t *testing.T) {
	client := &Client{
		runtime:      &errorRuntimeClient{},
		defaultModel: "test-model",
		maxTok:       10,
		temp:         0.5,
		think:        defaultThinkingBudget,
	}
	req := model.Request{
		ModelClass: model.ModelClassDefault,
		Messages: []*model.Message{
			{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}},
		},
		StructuredOutput: &model.StructuredOutput{
			Name:   "draft",
			Schema: []byte(`{"type":"object"}`),
		},
	}

	stream, err := client.Stream(context.Background(), &req)
	require.ErrorIs(t, err, model.ErrStructuredOutputUnsupported)
	require.Nil(t, stream)
}
