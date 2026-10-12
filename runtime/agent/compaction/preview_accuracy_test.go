package compaction

import (
	"context"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreviewEstimateBoundsEncodedResult(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content any
	}{
		{name: "plain text", content: strings.Repeat("plain text ", 500)},
		{name: "JSON-like text", content: strings.Repeat(`{"key":"value","path":"C:\\tmp"}`, 200)},
		{name: "structured JSON", content: map[string]any{"items": strings.Repeat(`"quoted"\path`, 400)}},
		{name: "control characters", content: strings.Repeat("\x00\x01\x02\t\n\r", 500)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.content, json.Deterministic(true))
			require.NoError(t, err)
			entry := index.MessageEntry{ID: 1, Kind: index.ToolResult, Hash: "result", ToolUseIDs: []string{"a"}, Bytes: len(data) + 1, Keep: true, Result: &index.ResultProjection{ContentBytes: len(data)}}
			view := index.ViewRef{PreviewThrough: 1, Budget: index.RenderBudget{PreviewTokens: 128}}
			projected, err := checkpoint.ResultText(t.Context(), func(context.Context, runstore.Hash) ([]byte, error) {
				return data, nil
			}, []index.MessageEntry{entry}, view)
			require.NoError(t, err)
			require.Contains(t, projected, entry.ID)
			measured, err := contextmeasure.Measure(&model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "a", Content: projected[entry.ID]}}}}}, model.ContextLimits{}, nil)
			require.NoError(t, err)
			estimated, nontext := entrySize(entry, view)
			assert.GreaterOrEqual(t, estimated, measured.TextBytes, "the estimate must bound the JSON-encoded preview, including framing")
			assert.Zero(t, nontext)
		})
	}
}
