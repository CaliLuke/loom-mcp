package agenttest

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultimodalRequestAndMatch(t *testing.T) {
	part := model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte{1, 2, 3}}
	request, err := NewRequest("model", &model.Request{Messages: []*model.Message{
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "context.read", Input: map[string]any{}}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: "descriptor", Parts: []model.ToolResultContentPart{part}}}},
	}})
	require.NoError(t, err)
	decoded, err := request.Request()
	require.NoError(t, err)
	assert.Equal(t, []model.ToolResultContentPart{part}, decoded.Messages[1].Parts[0].(model.ToolResultPart).Parts)
	require.NoError(t, LastToolResultParts(tools.Ident("context.read"), part)(decoded))
	assert.Error(t, LastToolResultParts(tools.Ident("context.read"))(decoded))
}
