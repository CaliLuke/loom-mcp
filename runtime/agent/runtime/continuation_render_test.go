package runtime

import (
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/continuation"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContinuationCarryDropsHiddenCalls(t *testing.T) {
	rt := New()
	data, err := json.Marshal(&model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "hidden", Name: "tool", Input: map[string]string{"q": "hidden"}}}})
	require.NoError(t, err)
	hash, err := rt.RunStore.Put(t.Context(), "run", data)
	require.NoError(t, err)
	entries := []index.MessageEntry{
		{ID: 1, Kind: index.Message, Hash: hash, Role: model.ConversationRoleAssistant, ToolUseIDs: []string{"hidden"}},
		{ID: 2, Kind: index.Structural, ToolUseIDs: []string{"hidden"}, Structural: &index.StructuralEntry{Kind: index.HideBookkeeping, ProtocolVersion: index.TemplateProtocolVersion}},
	}
	source := func(ctx context.Context, h runstore.Hash) ([]byte, error) {
		return rt.RunStore.Get(ctx, "run", h)
	}
	carried, err := carryEntries(t.Context(), source, entries, index.ViewRef{})
	require.NoError(t, err)
	for _, entry := range carried {
		assert.Empty(t, entry.Messages)
	}
}

func TestContinuationCarriedErrorFlag(t *testing.T) {
	rt := New()
	in := &api.SealRecordInput{RootRunID: "run", RunID: "run", Pos: 1}
	result, err := rt.storeCarried(t.Context(), in, continuation.Entry{Entry: index.MessageEntry{ID: 1, Bytes: 100}, Form: continuation.Placeholder, Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "failed", Content: "[result cleared]", IsError: true}}}}})
	require.NoError(t, err)
	messages, err := index.Render(t.Context(), []index.MessageEntry{result.Entry}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	assert.True(t, messages[0].Parts[0].(model.ToolResultPart).IsError)
}

func TestContinuationPreservesResultBatch(t *testing.T) {
	for _, form := range []string{continuation.Full, continuation.Placeholder} {
		t.Run(form, func(t *testing.T) {
			rt := New()
			in := &api.SealRecordInput{RootRunID: "old", RunID: "old", Pos: 1}
			carried := make([]session.Carried, 0, 2)
			blobs := make([][]byte, 0, 2)
			for i, id := range []string{"a", "b"} {
				item, err := rt.storeCarried(t.Context(), in, continuation.Entry{Entry: index.MessageEntry{ID: index.MessageID(i + 2), Kind: index.ToolResult, Batch: 2, ToolUseIDs: []string{id}}, Form: form, Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: id, Content: "rendered"}}}}})
				require.NoError(t, err)
				carried = append(carried, item)
				var data []byte
				if item.Bytes != nil {
					data, err = rt.RunStore.Get(t.Context(), "old", *item.Bytes)
					require.NoError(t, err)
				}
				blobs = append(blobs, data)
			}
			entries, _, err := rt.copyContinuationEntries(t.Context(), &api.RecordContentInput{RootRunID: "new"}, session.Continuation{}, carried, blobs, &api.ContinuationStartOutput{})
			require.NoError(t, err)
			require.Len(t, entries, 2)
			assert.Equal(t, index.MessageID(1), entries[0].Batch)
			assert.Equal(t, index.MessageID(1), entries[1].Batch)
			messages, err := index.Render(t.Context(), entries, func(ctx context.Context, h runstore.Hash) ([]byte, error) {
				return rt.RunStore.Get(ctx, "new", h)
			})
			require.NoError(t, err)
			require.Len(t, messages, 1)
			assert.Len(t, messages[0].Parts, 2)
		})
	}
}

func TestContinuationWriterMeasurements(t *testing.T) {
	rt := New()
	in := &api.SealRecordInput{RootRunID: "root", RunID: "root", Pos: 1}
	carried, err := rt.storeCarried(t.Context(), in, continuation.Entry{Entry: index.MessageEntry{ID: 1}, Form: continuation.Full, Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "measured"}}}}})
	require.NoError(t, err)
	assert.Equal(t, 8, carried.Entry.Bytes)
	users, err := rt.storeCarriedUsers(t.Context(), in, nil, []checkpoint.UserLine{{ID: 1, Text: "abc", Bytes: 999}})
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, 3, users[0].Entry.Bytes)
	data, err := json.Marshal(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}})
	require.NoError(t, err)
	entries, _, err := rt.appendContinuationInput(t.Context(), "root", nil, [][]byte{data})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 5, entries[0].Bytes)
}

func TestContinuationReasoningOnlyMessagesDoNotReachProvider(t *testing.T) {
	for _, parts := range [][]model.Part{nil, {model.TextPart{Text: "answer"}}, {model.ToolUsePart{ID: "call", Name: "tool", Input: map[string]any{}}}} {
		rt := New()
		messages := []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ThinkingPart{Redacted: []byte("provider-issued")}}}}
		if len(parts) > 0 {
			messages = append(messages, &model.Message{Role: model.ConversationRoleAssistant, Parts: parts})
		}
		plan, err := continuation.Carry([]continuation.Entry{{Entry: index.MessageEntry{ID: 1, Kind: index.Assistant, Role: model.ConversationRoleAssistant}, Messages: messages}}, index.ViewRef{}, 10000, 100)
		require.NoError(t, err)
		require.Len(t, plan.Tail, 1)
		carried, err := rt.storeCarried(t.Context(), &api.SealRecordInput{RootRunID: "run", RunID: "run", Pos: 1}, plan.Tail[0])
		require.NoError(t, err)
		rendered, err := index.Render(t.Context(), []index.MessageEntry{carried.Entry}, func(ctx context.Context, h runstore.Hash) ([]byte, error) {
			return rt.RunStore.Get(ctx, "run", h)
		})
		require.NoError(t, err)
		if len(parts) == 0 {
			assert.Empty(t, rendered)
		} else {
			require.Len(t, rendered, 1)
			assert.Equal(t, parts, rendered[0].Parts)
		}
		assert.Len(t, messages[0].Parts, 1)
	}
}
