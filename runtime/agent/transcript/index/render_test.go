package index_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStructuralTextBoundAndReferences(t *testing.T) {
	store := inmem.New()
	long := strings.Repeat("x", index.MaxInlineTextBytes+1)
	hash, err := store.Put(t.Context(), "root", []byte(long))
	require.NoError(t, err)
	source := func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
		return store.Get(ctx, "root", hash)
	}
	for _, tc := range []struct {
		name string
		text index.TextParameter
		want string
		fail bool
	}{
		{"at limit", index.TextParameter{Text: long[:index.MaxInlineTextBytes]}, long[:index.MaxInlineTextBytes], false},
		{"over limit", index.TextParameter{Text: long}, "", true},
		{"reference", index.TextParameter{Hash: hash}, long, false},
		{"ambiguous", index.TextParameter{Text: "inline", Hash: hash}, "", true},
		{"missing", index.TextParameter{Hash: "missing"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := index.MessageEntry{ID: 1, Kind: index.Structural, Structural: &index.StructuralEntry{
				Kind: index.Reminders, ProtocolVersion: 5, Text: []index.TextParameter{tc.text},
			}}
			got, err := index.Render(t.Context(), []index.MessageEntry{entry}, source)
			if tc.fail {
				require.Error(t, err)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, []*model.Message{{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "<system-reminder>" + tc.want + "</system-reminder>"}}}}, got)
		})
	}
}

func TestRenderFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry index.MessageEntry
	}{
		{"unknown kind", index.MessageEntry{ID: 1, Kind: "new"}},
		{"zero id", index.MessageEntry{Kind: index.Message, Hash: "content"}},
		{"missing content", index.MessageEntry{ID: 1, Kind: index.Message}},
		{"bad content", index.MessageEntry{ID: 1, Kind: index.Message, Hash: "content"}},
		{"future fields", index.MessageEntry{ID: 1, Kind: index.Message, Keep: true}},
		{"missing template", index.MessageEntry{ID: 1, Kind: index.Structural}},
		{"unknown template", index.MessageEntry{ID: 1, Kind: index.Structural, Structural: &index.StructuralEntry{Kind: "new", ProtocolVersion: 5}}},
		{"wrong version", index.MessageEntry{ID: 1, Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.FinalizeHint, ProtocolVersion: 4}}},
		{"missing argument", index.MessageEntry{ID: 1, Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.UnknownTool, ProtocolVersion: 5}}},
		{"missing result batch", index.MessageEntry{ID: 1, Kind: index.ToolResult, ToolUseIDs: []string{"id"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := index.Render(t.Context(), []index.MessageEntry{tc.entry}, func(context.Context, runstore.Hash) ([]byte, error) {
				return []byte(`invalid`), nil
			})
			require.Error(t, err)
			assert.Nil(t, got)
		})
	}
}

func TestRenderDoesNotReadRecordOrTypedPayload(t *testing.T) {
	entries := []index.MessageEntry{
		{ID: 1, Kind: index.TypedInput, Hash: "typed", Record: []runstore.Hash{"server"}},
		{ID: 2, Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.PolicyDenial, ProtocolVersion: 5}},
	}
	messages, err := index.Render(t.Context(), entries, func(context.Context, runstore.Hash) ([]byte, error) {
		t.Error("model-facing render must not read typed or server-only data")
		return nil, errors.New("forbidden content")
	})
	require.NoError(t, err)
	assert.Empty(t, messages)
}

func TestRenderPropagatesContentFailure(t *testing.T) {
	missing := errors.New("content unavailable")
	_, err := index.Render(t.Context(), []index.MessageEntry{{ID: 1, Kind: index.Assistant, Calls: []runstore.Hash{"call"}}}, func(context.Context, runstore.Hash) ([]byte, error) {
		return nil, missing
	})
	require.ErrorIs(t, err, missing)
}

func TestViewRefOmitsReservedFields(t *testing.T) {
	data, err := json.Marshal(index.ViewRef{IndexHead: "head"}, json.Deterministic(true))
	require.NoError(t, err)
	assert.JSONEq(t, `{"index_head":"head"}`, string(data))
}
