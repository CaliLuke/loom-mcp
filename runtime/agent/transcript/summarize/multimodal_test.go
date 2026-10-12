package summarize

import (
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultimodalRecallSummaryIgnoresParts(t *testing.T) {
	data, err := json.Marshal(model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: "source descriptor", Parts: []model.ToolResultContentPart{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte("private-image")}}}}})
	require.NoError(t, err)
	evidence, err := RenderInput(Input{From: 1, To: 2, AcceptsImages: true, AcceptsDocuments: true, Entries: []index.MessageEntry{{ID: 1, Kind: index.Message, Hash: "message"}}, Contents: map[runstore.Hash][]byte{"message": data}})
	require.NoError(t, err)
	require.Len(t, evidence.Units, 1)
	assert.Equal(t, `"source descriptor"`, evidence.Units[0].Text)
	assert.Nil(t, evidence.Units[0].Attachment)
}
