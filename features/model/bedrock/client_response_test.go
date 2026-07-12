package bedrock

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
)

func TestTranslateResponseRejectsUnmarshalableToolInput(t *testing.T) {
	input := any(map[string]any{"": "invalid"})
	output := toolUseResponse(input)

	resp, err := translateResponse(output, nil, "test-model", model.ModelClassDefault)
	require.Nil(t, resp)
	require.ErrorContains(t, err, "bedrock: marshal tool input")
}

func TestTranslateResponseDecodesToolInput(t *testing.T) {
	output := toolUseResponse(map[string]any{"query": "loom"})

	resp, err := translateResponse(output, map[string]string{"lookup": "search.lookup"}, "test-model", model.ModelClassDefault)
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	require.Equal(t, "search.lookup", resp.ToolCalls[0].Name.String())
	require.Equal(t, "call-1", resp.ToolCalls[0].ID)
	require.JSONEq(t, `{"query":"loom"}`, string(resp.ToolCalls[0].Payload))
}

func toolUseResponse(input any) *bedrockruntime.ConverseOutput {
	return &bedrockruntime.ConverseOutput{
		Output: &brtypes.ConverseOutputMemberMessage{
			Value: brtypes.Message{
				Content: []brtypes.ContentBlock{
					&brtypes.ContentBlockMemberToolUse{Value: brtypes.ToolUseBlock{
						Name:      aws.String("lookup"),
						ToolUseId: aws.String("call-1"),
						Input:     document.NewLazyDocument(&input),
					}},
				},
			},
		},
	}
}
