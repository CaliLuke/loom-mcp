package bedrock

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type errorRuntimeClient struct {
	converseErr         error
	converseStreamErr   error
	converseInput       *bedrockruntime.ConverseInput
	converseStreamInput *bedrockruntime.ConverseStreamInput
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
	input *bedrockruntime.ConverseStreamInput,
	_ ...func(*bedrockruntime.Options),
) (*bedrockruntime.ConverseStreamOutput, error) {
	e.converseStreamInput = input
	return nil, e.converseStreamErr
}

func TestIsRateLimited_IdempotentOnSentinel(t *testing.T) {
	err := model.ErrRateLimited
	require.True(t, isRateLimited(err))

	wrapped := fmt.Errorf("provider: %w", err)
	require.True(t, isRateLimited(wrapped))
}

func TestIsRateLimitedRecognizesSmithyErrorShapes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "throttling exception",
			err:  &smithy.GenericAPIError{Code: bedrockThrottlingCode, Message: "slow down"},
			want: true,
		},
		{
			name: "too many requests exception",
			err:  &smithy.GenericAPIError{Code: "TooManyRequestsException", Message: "slow down"},
			want: true,
		},
		{
			name: "other API error",
			err:  &smithy.GenericAPIError{Code: "ValidationException", Message: "invalid"},
		},
		{
			name: "HTTP 429",
			err: &smithyhttp.ResponseError{
				Response: &smithyhttp.Response{Response: &http.Response{StatusCode: http.StatusTooManyRequests}},
				Err:      errors.New("throttled"),
			},
			want: true,
		},
		{
			name: "HTTP 500",
			err: &smithyhttp.ResponseError{
				Response: &smithyhttp.Response{Response: &http.Response{StatusCode: http.StatusInternalServerError}},
				Err:      errors.New("failed"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isRateLimited(tc.err))
		})
	}
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

func TestStream_SetsStructuredOutputConfig(t *testing.T) {
	rt := &errorRuntimeClient{converseStreamErr: model.ErrRateLimited}
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

	stream, err := client.Stream(context.Background(), &req)
	require.ErrorIs(t, err, model.ErrRateLimited)
	require.Nil(t, stream)
	require.NotNil(t, rt.converseStreamInput)
	require.NotNil(t, rt.converseStreamInput.OutputConfig)
	require.NotNil(t, rt.converseStreamInput.OutputConfig.TextFormat)
}
