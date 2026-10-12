package ollama

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolResultPartsUnsupported(t *testing.T) {
	client := &Client{}
	req := &model.Request{Model: "test-model", Messages: []*model.Message{{Role: "user", Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: "ok", Parts: []model.ToolResultContentPart{model.ImagePart{Format: "png", Bytes: []byte{1}}}}}}}}
	_, err := client.buildChatRequest(req, false)
	require.ErrorIs(t, err, model.ErrUnsupportedPart)
	var typed *model.UnsupportedPartError
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, "ollama", typed.Provider)
	assert.Equal(t, "tool_result", typed.Placement)
	// A nil transport/SDK is deliberate: rejection must precede any provider I/O.
	_, err = client.Complete(t.Context(), req)
	require.ErrorIs(t, err, model.ErrUnsupportedPart)
	_, err = client.Stream(t.Context(), req)
	require.ErrorIs(t, err, model.ErrUnsupportedPart)
}
