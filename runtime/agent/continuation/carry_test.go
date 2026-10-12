package continuation

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCarryKeepResultAndArguments(t *testing.T) {
	entries := []Entry{
		{Entry: index.MessageEntry{ID: 1, Kind: index.Assistant, Role: model.ConversationRoleAssistant, ToolUseIDs: []string{"call"}}, Messages: []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "tool", Input: map[string]any{"text": strings.Repeat("argument", 10000)}}}}}},
		{Entry: index.MessageEntry{ID: 2, Kind: index.ToolResult, Keep: true, Batch: 2, ToolUseIDs: []string{"call"}, Bytes: 1000000}, Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: strings.Repeat("SECRET", 170000)}}}}},
		{Entry: index.MessageEntry{ID: 3, Kind: index.Message, Role: model.ConversationRoleUser}, Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: strings.Repeat("last", 10000)}}}}},
	}
	plan, err := Carry(entries, index.ViewRef{}, 2048, 256)
	require.NoError(t, err)
	assert.True(t, plan.Bytes <= 2048 || plan.Minimal)
	data, err := json.Marshal(plan.Tail)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "SECRET")
	assert.Contains(t, string(data), "preview")
	assert.Contains(t, string(data), "placeholder")
	var args map[string]any
	b, err := json.Marshal(plan.Tail[0].Messages[0].Parts[0].(model.ToolUsePart).Input)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &args))
	assert.Contains(t, args, "preview")
}

func TestVisibleHashRoundTrip(t *testing.T) {
	msg := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}, Meta: map[string]any{"request": "one"}}
	a, err := VisibleHash(msg)
	require.NoError(t, err)
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	var restored model.Message
	require.NoError(t, json.Unmarshal(data, &restored))
	restored.Meta = nil
	restored.Parts = append(restored.Parts, model.CacheCheckpointPart{})
	b, err := VisibleHash(&restored)
	require.NoError(t, err)
	assert.Equal(t, a, b)
	assert.Equal(t, runstore.Hash("sha256:a09bb432e3e666a64cf18c5a57189a9fc06b7feadfb175cd6e293e4adcf3bc69"), a)
	assert.True(t, Match([]runstore.Hash{a}, []runstore.Hash{a, b}))
}
