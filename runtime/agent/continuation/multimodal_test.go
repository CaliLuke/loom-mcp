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

func TestDefaultCarryNamesNonTextParts(t *testing.T) {
	plan, err := Carry([]Entry{{Entry: index.MessageEntry{ID: 7, Kind: index.Message, NonText: 1500}, Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "look"}, model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("image")}, model.DocumentPart{Name: "document", Format: model.DocumentFormatPDF, Bytes: []byte("document")}}}}}}, index.ViewRef{}, 100000, 100)
	require.NoError(t, err)
	require.Len(t, plan.Tail, 1)
	assert.Zero(t, plan.Tail[0].Entry.NonText)
	var text string
	for _, p := range plan.Tail[0].Messages[0].Parts {
		if v, ok := p.(model.TextPart); ok {
			text += v.Text
		}
	}
	assert.Contains(t, text, "m7")
	assert.Contains(t, text, "part 2")
	assert.Contains(t, text, "png")
	assert.Contains(t, text, "5 bytes")
	assert.Contains(t, text, "part 3")
	assert.Contains(t, text, "pdf")
	assert.Contains(t, text, "8 bytes")
	assert.NotContains(t, text, "non-text content omitted")
	assert.LessOrEqual(t, plan.Bytes, 100000)
}
