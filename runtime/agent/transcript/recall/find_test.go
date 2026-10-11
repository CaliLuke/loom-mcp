package recall

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

func TestHandlerFindMatchReadPart(t *testing.T) {
	for _, tc := range []struct {
		name, needle, id string
		part             Part
	}{
		{"arguments", "argument needle", "call", Arguments},
		{"result", "result needle", "call", Result},
		{"user", "user needle", "3", Result},
		{"assistant", "assistant needle", "1", Result},
		{"seeded arguments", "seeded needle", "seed", Arguments},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecallFixture(t)
			call := f.put(map[string]any{"Name": "svc.search", "ToolCallID": "call", "Payload": map[string]string{"query": "prefix argument needle"}})
			f.entries(
				index.MessageEntry{Kind: index.Assistant, Hash: f.put([]*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "prefix assistant needle"}}}}), Calls: []runstore.Hash{call}, ToolUseIDs: []string{"call"}},
				index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte("prefix result needle")), ToolUseIDs: []string{"call"}},
				index.MessageEntry{Kind: index.Message, Role: model.ConversationRoleUser, Hash: f.put(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "prefix user needle"}}})},
				index.MessageEntry{Kind: index.Message, Role: model.ConversationRoleAssistant, Hash: f.put(&model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "seed", Name: "svc.search", Input: map[string]string{"query": "prefix seeded needle"}}}})},
			)
			f.req.View.TailFrom = 5
			found := f.call(stringJSON(t, Input{Find: &FindInput{Text: tc.needle}}))
			require.Len(t, found.Matches, 1)
			// Decode the model-facing JSON so this regression runs before Match.Part exists.
			var match struct {
				ID, Run string
				Part    Part
				Offset  int
			}
			require.NoError(t, json.Unmarshal([]byte(stringJSON(t, found.Matches[0])), &match, json.MatchCaseInsensitiveNames(true)))
			assert.Equal(t, tc.id, match.ID)
			assert.Equal(t, tc.part, match.Part, "find must supply the part needed by read")
			read := f.call(stringJSON(t, Input{Read: &ReadInput{ID: match.ID, Run: match.Run, Part: match.Part, Offset: match.Offset}}))
			assert.Empty(t, read.Code)
			assert.True(t, strings.HasPrefix(read.Text, tc.needle), "read must start at the matched source text: %q", read.Text)
		})
	}
}

func TestHandlerFindContinuationAcrossCompaction(t *testing.T) {
	for _, clearing := range []bool{false, true} {
		name := "tail eviction"
		if clearing {
			name = "result clearing"
		}
		t.Run(name, func(t *testing.T) {
			f := newRecallFixture(t)
			f.entries(
				index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte("needle oldest")), ToolUseIDs: []string{"old"}},
				index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte(strings.Repeat("needle middle ", 11))), ToolUseIDs: []string{"middle"}},
				index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte("needle newly hidden")), ToolUseIDs: []string{"new"}},
			)
			if clearing {
				f.req.View.ClearedThrough = 2
			} else {
				f.req.View.TailFrom = 3
			}
			first := f.call(`{"find":{"text":"needle"}}`)
			require.Len(t, first.Matches, 10)
			require.NotEmpty(t, first.Cursor)
			if clearing {
				f.req.View.ClearedThrough = 3
			} else {
				f.req.View.TailFrom = 4
			}
			second := f.call(stringJSON(t, Input{Find: &FindInput{Text: "needle", Cursor: first.Cursor}}))
			require.Len(t, second.Matches, 2, "compaction must not shift the continuation into already returned matches")
			assert.Equal(t, "middle", second.Matches[0].ID)
			assert.Equal(t, 140, second.Matches[0].Offset)
			assert.Equal(t, "old", second.Matches[1].ID)
			assert.Equal(t, 0, second.Matches[1].Offset)
			assert.Empty(t, second.Cursor)
			fresh := f.call(`{"find":{"text":"needle"}}`)
			require.NotEmpty(t, fresh.Matches)
			assert.Equal(t, "new", fresh.Matches[0].ID, "fresh searches include newly hidden content")
		})
	}
}

func TestHandlerFindContinuationAcrossChunks(t *testing.T) {
	f := newRecallFixture(t)
	call := f.put(map[string]any{"Name": "svc.search", "ToolCallID": "call", "Payload": map[string]string{"query": strings.Repeat("needle ", 11)}})
	f.entries(
		index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte("needle oldest")), ToolUseIDs: []string{"old"}},
		index.MessageEntry{Kind: index.Assistant, Hash: f.put([]*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: strings.Repeat("needle ", 10)}}}}), Calls: []runstore.Hash{call}, ToolUseIDs: []string{"call"}},
		index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte("needle newly hidden")), ToolUseIDs: []string{"new"}},
	)
	f.req.View.TailFrom = 3
	first := f.call(`{"find":{"text":"needle"}}`)
	require.Len(t, first.Matches, 10)
	second := f.call(stringJSON(t, Input{Find: &FindInput{Text: "needle", Cursor: first.Cursor}}))
	require.Len(t, second.Matches, 10)
	assert.Equal(t, "call", second.Matches[0].ID)
	f.req.View.TailFrom = 4
	last := f.call(stringJSON(t, Input{Find: &FindInput{Text: "needle", Cursor: second.Cursor}}))
	require.Len(t, last.Matches, 2)
	assert.Equal(t, "call", last.Matches[0].ID)
	assert.Equal(t, 80, last.Matches[0].Offset)
	assert.Equal(t, "old", last.Matches[1].ID)
	assert.Empty(t, last.Cursor)
}

func TestHandlerFindCursorConfinement(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cursor Cursor
	}{
		{"foreign run", Cursor{Kind: Find, Scan: "needle", Anchor: &FindAnchor{Run: "foreign", Entry: 1}}},
		{"missing entry", Cursor{Kind: Find, Scan: "needle", Anchor: &FindAnchor{Run: "run", Entry: 99}}},
		{"visible entry", Cursor{Kind: Find, Scan: "needle", Anchor: &FindAnchor{Run: "run", Entry: 2}}},
		{"missing chunk", Cursor{Kind: Find, Scan: "needle", Anchor: &FindAnchor{Run: "run", Entry: 1, Chunk: 1}}},
		{"offset past chunk", Cursor{Kind: Find, Scan: "needle", Anchor: &FindAnchor{Run: "run", Entry: 1}, Position: 99}},
		{"wrong text", Cursor{Kind: Find, Scan: "other", Anchor: &FindAnchor{Run: "run", Entry: 1}}},
		{"wrong operation", Cursor{Kind: List, Handle: "rc-forged"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecallFixture(t)
			f.entries(
				index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte("needle hidden")), ToolUseIDs: []string{"old"}},
				index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte("needle visible")), ToolUseIDs: []string{"new"}},
			)
			f.req.View.TailFrom = 2
			token, err := EncodeCursor(tc.cursor)
			require.NoError(t, err)
			response := f.call(stringJSON(t, Input{Find: &FindInput{Text: "needle", Cursor: token}}))
			assert.Equal(t, CodeInvalidInput, response.Code)
			assert.Empty(t, response.Matches)
			assert.Empty(t, response.Cursor)
		})
	}
}
