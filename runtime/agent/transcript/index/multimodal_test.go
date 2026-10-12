package index

import (
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultimodalResultRender(t *testing.T) {
	image := model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte{1, 2, 3}}
	blob, err := json.Marshal(model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{image}})
	require.NoError(t, err)
	source := func(_ context.Context, h runstore.Hash) ([]byte, error) {
		if h == "parts" {
			return blob, nil
		}
		return []byte(`{"attachment":{"message":"1"}}`), nil
	}
	entry := MessageEntry{ID: 1, Kind: ToolResult, Hash: "result", Batch: 1, ToolUseIDs: []string{"call"}, NonText: 1500, Result: &ResultProjection{Parts: "parts"}}
	messages, err := Render(t.Context(), []MessageEntry{entry}, source)
	require.NoError(t, err)
	assert.Equal(t, []model.ToolResultContentPart{image}, messages[0].Parts[0].(model.ToolResultPart).Parts)
	messages, err = RenderTail(t.Context(), []MessageEntry{entry}, source, nil, map[MessageID]string{1: "cleared"})
	require.NoError(t, err)
	assert.Empty(t, messages[0].Parts[0].(model.ToolResultPart).Parts)
	messages, err = RenderTail(t.Context(), []MessageEntry{entry}, source, map[string]bool{"call": true}, nil)
	require.NoError(t, err)
	assert.Empty(t, messages)
	entry.Kind = Message
	_, err = Render(t.Context(), []MessageEntry{entry}, source)
	assert.Error(t, err)
}
