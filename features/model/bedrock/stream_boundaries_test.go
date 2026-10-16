package bedrock

import (
	"context"
	"io"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBedrockStreamRejectsMalformedBlockContracts(t *testing.T) {
	stop := &brtypes.ConverseStreamOutputMemberMessageStop{Value: brtypes.MessageStopEvent{StopReason: brtypes.StopReasonEndTurn}}
	toolStart := &brtypes.ConverseStreamOutputMemberContentBlockStart{Value: brtypes.ContentBlockStartEvent{ContentBlockIndex: aws.Int32(0), Start: &brtypes.ContentBlockStartMemberToolUse{Value: brtypes.ToolUseBlockStart{ToolUseId: aws.String("call"), Name: aws.String("lookup")}}}}
	tests := []struct {
		name       string
		structured bool
		events     []brtypes.ConverseStreamOutput
		want       string
	}{
		{"missing metadata", false, []brtypes.ConverseStreamOutput{stop}, "ended before metadata"},
		{"start missing index", false, []brtypes.ConverseStreamOutput{&brtypes.ConverseStreamOutputMemberContentBlockStart{}}, "index"},
		{"delta missing index", false, []brtypes.ConverseStreamOutput{&brtypes.ConverseStreamOutputMemberContentBlockDelta{}}, "index"},
		{"stop missing index", false, []brtypes.ConverseStreamOutput{&brtypes.ConverseStreamOutputMemberContentBlockStop{}}, "index"},
		{"tool missing id", false, []brtypes.ConverseStreamOutput{&brtypes.ConverseStreamOutputMemberContentBlockStart{Value: brtypes.ContentBlockStartEvent{ContentBlockIndex: aws.Int32(0), Start: &brtypes.ContentBlockStartMemberToolUse{Value: brtypes.ToolUseBlockStart{Name: aws.String("lookup")}}}}}, "missing tool_use_id"},
		{"tool missing name", false, []brtypes.ConverseStreamOutput{&brtypes.ConverseStreamOutputMemberContentBlockStart{Value: brtypes.ContentBlockStartEvent{ContentBlockIndex: aws.Int32(0), Start: &brtypes.ContentBlockStartMemberToolUse{Value: brtypes.ToolUseBlockStart{ToolUseId: aws.String("call")}}}}}, "missing name"},
		{"malformed arguments", false, []brtypes.ConverseStreamOutput{toolStart, bedrockToolDelta(0, "{"), &brtypes.ConverseStreamOutputMemberContentBlockStop{Value: brtypes.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)}}}, "payload"},
		{"structured tool start", true, []brtypes.ConverseStreamOutput{toolStart}, "emitted tool_use start"},
		{"structured tool delta", true, []brtypes.ConverseStreamOutput{bedrockToolDelta(0, "{}")}, "emitted tool_use delta"},
		{"structured multiple blocks", true, []brtypes.ConverseStreamOutput{bedrockTextDelta(0, "{"), bedrockTextDelta(1, "}")}, "multiple content blocks"},
		{"structured blank completion", true, []brtypes.ConverseStreamOutput{bedrockTextDelta(0, " "), stop}, "payload is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := make(chan brtypes.ConverseStreamOutput, len(tt.events)+1)
			source <- bedrockMessageStart()
			for _, event := range tt.events {
				source <- event
			}
			close(source)
			client := &Client{runtime: &conformanceRuntimeClient{}, defaultModel: "anthropic.claude-sonnet-4-20250514-v1:0", maxTok: 128, logger: telemetry.NewNoopLogger()}
			client.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (StreamOutput, error) {
				return newConformanceStreamOutput(&conformanceStreamReader{events: source}), nil
			}
			request := &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}}
			if tt.structured {
				request.StructuredOutput = &model.StructuredOutput{Name: "answer", Schema: []byte(`{"type":"object","properties":{},"additionalProperties":false}`)}
			}
			stream, err := client.Stream(t.Context(), request)
			require.NoError(t, err)
			t.Cleanup(func() {
				assert.NoError(t, stream.Close())
			})
			for range len(tt.events) + 3 {
				_, err = stream.Recv()
				if err != nil {
					break
				}
			}
			require.ErrorContains(t, err, tt.want)
			require.NotErrorIs(t, err, io.EOF)
			assert.Nil(t, stream.Response())
		})
	}
}
