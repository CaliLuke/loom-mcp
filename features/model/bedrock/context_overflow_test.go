package bedrock

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
)

// ValidationException bodies: AWS's validation guide and Claude context docs:
// https://repost.aws/knowledge-center/bedrock-validation-exception-errors
// https://platform.claude.com/docs/en/build-with-claude/context-windows
// Titan's captured token error: https://www.philschmid.de/amazon-titan-embeddings
var bedrockOverflowBodies = []string{
	`{"message":"Input is too long for requested model."}`,
	`{"message":"prompt is too long: 213462 tokens > 200000 maximum"}`,
	`{"message":"Too many input tokens. Max input tokens: 8192, request input token count: 18422"}`,
}

func testContextOverflow(t *testing.T) {
	for _, body := range bedrockOverflowBodies {
		t.Run(body, func(t *testing.T) {
			apiErr := bedrockBodyError(t, body, "ValidationException", 400)
			client := bedrockOverflowClient(&errorRuntimeClient{converseErr: apiErr})
			response, err := client.Complete(context.Background(), bedrockOverflowRequest())
			assert.Nil(t, response)
			pe := requireBedrockOverflow(t, err)
			assert.Equal(t, 400, pe.HTTPStatus())
			assert.Equal(t, "ValidationException", pe.Code())
			require.ErrorIs(t, err, apiErr)
		})
	}
	for _, tc := range []struct {
		name string
		body string
		code string
	}{
		{name: "ordinary 400", body: `{"message":"The provided model identifier is invalid."}`, code: "ValidationException"},
		{name: "wrong code", body: bedrockOverflowBodies[0], code: "BadRequestException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apiErr := bedrockBodyError(t, tc.body, tc.code, 400)
			response, err := bedrockOverflowClient(&errorRuntimeClient{converseErr: apiErr}).Complete(context.Background(), bedrockOverflowRequest())
			assert.Nil(t, response)
			var pe *model.ProviderError
			require.ErrorAs(t, err, &pe)
			assert.Equal(t, model.ProviderErrorKindInvalidRequest, pe.Kind())
			assert.False(t, pe.Retryable())
		})
	}
	t.Run("context window stop", func(t *testing.T) {
		// Converse response shape: https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html
		var response bedrockruntime.ConverseOutput
		require.NoError(t, json.Unmarshal([]byte(`{"stopReason":"model_context_window_exceeded","usage":{"inputTokens":199900,"outputTokens":100,"totalTokens":200000}}`), &response, json.MatchCaseInsensitiveNames(true)))
		client := bedrockOverflowClient(&conformanceRuntimeClient{converse: func(context.Context, *bedrockruntime.ConverseInput) (*bedrockruntime.ConverseOutput, error) {
			return &response, nil
		}})
		result, err := client.Complete(context.Background(), bedrockOverflowRequest())
		assert.Nil(t, result)
		assert.Equal(t, "model_context_window_exceeded", requireBedrockOverflow(t, err).Code())
	})
}

func testStreamContextOverflow(t *testing.T) {
	for _, body := range bedrockOverflowBodies {
		for _, phase := range []string{"setup", "receive"} {
			t.Run(phase+body, func(t *testing.T) {
				apiErr := bedrockBodyError(t, body, "ValidationException", 400)
				client := bedrockOverflowClient(&errorRuntimeClient{converseStreamErr: apiErr})
				if phase == "receive" {
					// Event-stream exceptions carry no HTTP response wrapper.
					apiErr = bedrockBodyError(t, body, "ValidationException", 0)
					client.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (StreamOutput, error) {
						ch := make(chan brtypes.ConverseStreamOutput)
						close(ch)
						return newConformanceStreamOutput(&conformanceStreamReader{events: ch, err: apiErr}), nil
					}
				}
				stream, err := client.Stream(context.Background(), bedrockOverflowRequest())
				if phase == "setup" {
					assert.Nil(t, stream)
					assert.Equal(t, "converse_stream", requireBedrockOverflow(t, err).Operation())
					return
				}
				require.NoError(t, err)
				defer func() { assert.ErrorIs(t, stream.Close(), apiErr) }()
				chunk, err := stream.Recv()
				assert.Nil(t, chunk)
				assert.Equal(t, "converse_stream.recv", requireBedrockOverflow(t, err).Operation())
				assert.Nil(t, stream.Response())
			})
		}
	}
	t.Run("context window stop", func(t *testing.T) {
		// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_MessageStopEvent.html
		var event brtypes.MessageStopEvent
		require.NoError(t, json.Unmarshal([]byte(`{"stopReason":"model_context_window_exceeded"}`), &event, json.MatchCaseInsensitiveNames(true)))
		client := bedrockOverflowClient(&conformanceRuntimeClient{})
		client.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (StreamOutput, error) {
			ch := make(chan brtypes.ConverseStreamOutput, 1)
			ch <- &brtypes.ConverseStreamOutputMemberMessageStop{Value: event}
			close(ch)
			return newConformanceStreamOutput(&conformanceStreamReader{events: ch}), nil
		}
		stream, err := client.Stream(context.Background(), bedrockOverflowRequest())
		require.NoError(t, err)
		defer func() { assert.NoError(t, stream.Close()) }()
		chunk, err := stream.Recv()
		assert.Nil(t, chunk)
		assert.Equal(t, "model_context_window_exceeded", requireBedrockOverflow(t, err).Code())
		assert.Nil(t, stream.Response())
	})
}

func bedrockBodyError(t *testing.T, body, code string, status int) error {
	t.Helper()
	var wire struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &wire))
	apiErr := &smithy.GenericAPIError{Code: code, Message: wire.Message, Fault: smithy.FaultClient}
	if status == 0 {
		return apiErr
	}
	return &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status}}, Err: apiErr}
}

func bedrockOverflowClient(runtime RuntimeClient) *Client {
	return &Client{runtime: runtime, defaultModel: "bedrock-sonnet", maxTok: 128, logger: telemetry.NewNoopLogger()}
}

func bedrockOverflowRequest() *model.Request {
	return &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}}
}

func requireBedrockOverflow(t *testing.T, err error) *model.ProviderError {
	t.Helper()
	var pe *model.ProviderError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, model.ProviderErrorKindContextOverflow, pe.Kind())
	assert.Equal(t, "bedrock", pe.Provider())
	assert.False(t, pe.Retryable())
	return pe
}
