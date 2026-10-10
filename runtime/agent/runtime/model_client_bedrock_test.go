package runtime

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type bedrockQueryEngine struct {
	stubEngine
	t *testing.T
}

var _ interface {
	QueryWorkflow(context.Context, string, string, ...any) (converter.EncodedValue, error)
} = (*bedrockQueryEngine)(nil)

func TestNewBedrockModelClientUsesOnlyPlannerMessages(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		path   string
		invoke func(context.Context, model.Client, *model.Request) error
	}{
		{
			name: "complete",
			path: "/model/test-model/converse",
			invoke: func(ctx context.Context, client model.Client, req *model.Request) error {
				_, err := client.Complete(ctx, req)
				return err
			},
		},
		{
			name: "stream",
			path: "/model/test-model/converse-stream",
			invoke: func(ctx context.Context, client model.Client, req *model.Request) error {
				_, err := client.Stream(ctx, req)
				return err
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var encoded struct {
				Messages jsontext.Value `json:"messages"`
				System   jsontext.Value `json:"system"`
			}
			transportErr := errors.New("request captured")
			awsrt := bedrockruntime.New(bedrockruntime.Options{
				Region: "us-east-1",
				Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
					return aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, nil
				}),
				RetryMaxAttempts: 1,
				HTTPClient: smithyhttp.ClientDoFunc(func(req *http.Request) (*http.Response, error) {
					assert.Equal(t, tt.path, req.URL.Path)
					require.NoError(t, json.UnmarshalRead(req.Body, &encoded))
					return nil, transportErr
				}),
			})
			rt := &Runtime{Engine: &bedrockQueryEngine{t: t}}
			client, err := rt.NewBedrockModelClient(awsrt, BedrockConfig{DefaultModel: "test-model"})
			require.NoError(t, err)
			require.NotNil(t, client)
			req := &model.Request{
				RunID: "run-123",
				Messages: []*model.Message{
					{
						Role:  model.ConversationRoleSystem,
						Parts: []model.Part{model.TextPart{Text: "planner instructions"}},
					},
					{
						Role:  model.ConversationRoleUser,
						Parts: []model.Part{model.TextPart{Text: "first question"}},
					},
					{
						Role:  model.ConversationRoleAssistant,
						Parts: []model.Part{model.TextPart{Text: "first answer"}},
					},
					{
						Role:  model.ConversationRoleUser,
						Parts: []model.Part{model.TextPart{Text: "next question"}},
					},
				},
			}

			err = tt.invoke(t.Context(), client, req)

			require.ErrorIs(t, err, transportErr)
			assert.JSONEq(t, `[
				{"role":"user","content":[{"text":"first question"}]},
				{"role":"assistant","content":[{"text":"first answer"}]},
				{"role":"user","content":[{"text":"next question"}]}
			]`, string(encoded.Messages))
			assert.JSONEq(t, `[{"text":"planner instructions"}]`, string(encoded.System))
		})
	}
}

func (e *bedrockQueryEngine) QueryWorkflow(context.Context, string, string, ...any) (converter.EncodedValue, error) {
	e.t.Error("Bedrock request encoding must not query the workflow")
	return nil, errors.New("unexpected workflow query")
}
