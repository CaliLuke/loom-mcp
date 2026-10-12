package continuation

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultimodalCarryDropsNewestResult(t *testing.T) {
	result := model.ToolResultPart{ToolUseID: "call", Content: "descriptor", Parts: []model.ToolResultContentPart{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("secret-image")}}}
	plan, err := Carry([]Entry{{Entry: index.MessageEntry{ID: 1, Kind: index.ToolResult, NonText: 1500}, Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{result}}}}}, index.ViewRef{}, 100000, 100)
	require.NoError(t, err)
	require.Len(t, plan.Tail, 1)
	assert.Empty(t, plan.Tail[0].Messages[0].Parts[0].(model.ToolResultPart).Parts)
	assert.Equal(t, Preview, plan.Tail[0].Form)
	assert.Zero(t, plan.Tail[0].Entry.NonText)
	assert.Len(t, result.Parts, 1)
}
