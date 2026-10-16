package checkpoint

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectPreservesSystemPrefixAndFailsOnMissingEvidence(t *testing.T) {
	entries := []index.MessageEntry{
		{ID: 1, Kind: index.Message, Role: model.ConversationRoleSystem, Hash: "system", Chain: "one"},
		{ID: 2, Kind: index.Message, Role: model.ConversationRoleUser, Hash: "user", Chain: "two"},
		{ID: 3, Kind: index.Assistant, Role: model.ConversationRoleAssistant, Hash: "assistant", Chain: "three"},
		{ID: 4, Kind: index.Message, Role: model.ConversationRoleAssistant, Hash: "tail", Chain: "four"},
	}
	source := recordSource(t, map[runstore.Hash]any{
		"system":     &model.Message{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "system rule"}}},
		"user":       &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "original question"}}},
		"tail":       &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "follow up"}}},
		"checkpoint": index.CheckpointRecord{Through: 3, Chain: "three", Summary: hashPointer("summary")},
	}, map[runstore.Hash][]byte{"summary": []byte("prior conversation")})
	view := index.ViewRef{Checkpoint: hashPointer("checkpoint"), TailFrom: 4, Budget: index.RenderBudget{KeepUserTokens: 300, MaxSummaryTokens: 300, ReceiptTokens: 300, FramingTokens: 300}}
	messages, err := Project(t.Context(), source, entries, view)
	require.NoError(t, err)
	require.Len(t, messages, 3)
	assert.Equal(t, model.ConversationRoleSystem, messages[0].Role)
	assert.Equal(t, []model.Part{model.TextPart{Text: "system rule"}}, messages[0].Parts)
	assert.Contains(t, messages[1].Parts[0].(model.TextPart).Text, "prior conversation")
	assert.Equal(t, []model.Part{model.TextPart{Text: "follow up"}}, messages[2].Parts)
	for _, missing := range []runstore.Hash{"system", "user", "tail", "checkpoint", "summary"} {
		t.Run(string(missing), func(t *testing.T) {
			failed := func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
				if hash == missing {
					return nil, assert.AnError
				}
				return source(ctx, hash)
			}
			messages, err := Project(t.Context(), failed, entries, view)
			require.ErrorIs(t, err, assert.AnError)
			assert.Nil(t, messages)
		})
	}
	tiny := view
	tiny.Budget = index.RenderBudget{}
	messages, err = Project(t.Context(), source, entries, tiny)
	require.ErrorIs(t, err, ErrCheckpointBudget)
	assert.Nil(t, messages)
}

func TestResultTextRejectsIncompleteClearAndPreviewEvidence(t *testing.T) {
	for _, tt := range []struct {
		name  string
		entry index.MessageEntry
		view  index.ViewRef
	}{
		{"missing cleared result", index.MessageEntry{ID: 1, Kind: index.ToolResult}, index.ViewRef{ClearedThrough: 1}},
		{"missing forced result", index.MessageEntry{ID: 1, Kind: index.ToolResult, Keep: true}, index.ViewRef{ForceClearThrough: 1}},
		{"missing error evidence", index.MessageEntry{ID: 1, Kind: index.ToolResult, Hash: "body", ToolUseIDs: []string{"call"}, Result: &index.ResultProjection{Error: &index.TextParameter{Hash: "error"}}}, index.ViewRef{ClearedThrough: 1}},
		{"missing preview evidence", index.MessageEntry{ID: 1, Kind: index.ToolResult, Hash: "body", ToolUseIDs: []string{"call"}}, index.ViewRef{PreviewThrough: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			projected, err := ResultText(t.Context(), func(context.Context, runstore.Hash) ([]byte, error) {
				return nil, assert.AnError
			}, []index.MessageEntry{tt.entry}, tt.view)
			require.Error(t, err)
			assert.Nil(t, projected)
		})
	}
}
