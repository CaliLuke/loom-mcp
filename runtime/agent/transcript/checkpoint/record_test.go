package checkpoint

import (
	"context"
	"encoding/json/v2"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromRecordCoverageAndSummary(t *testing.T) {
	previous, summary := runstore.Hash("previous"), runstore.Hash("summary")
	entries := []index.MessageEntry{{ID: 1, Kind: index.Message, Role: model.ConversationRoleUser, Hash: "user", Bytes: 4, Chain: "one"}, {ID: 2, Kind: index.Assistant, Role: model.ConversationRoleAssistant, ToolUseIDs: []string{"call"}, Chain: "two"}, {ID: 3, Kind: index.ToolResult, Role: model.ConversationRoleUser, ToolUseIDs: []string{"call"}, Chain: "three"}, {ID: 4, Kind: index.Clarification, Role: model.ConversationRoleUser, Hash: "answer", Bytes: 6, Chain: "four"}, {ID: 5, Kind: index.Assistant, Role: model.ConversationRoleAssistant, Chain: "five"}}
	cited := []index.IndexLine{{ID: 1, Text: "task"}}
	old := index.CheckpointRecord{Summary: &summary, SummaryRun: "run", CitedUsers: cited, Through: 3, Chain: "three", Receipt: []index.ReceiptLine{{Kind: "call", From: 2, Through: 3, Tool: "search", ToolUseID: "call", Text: "args", Unsummarised: []index.ByteRange{{From: 1, Through: 9}}}}}
	// Phase 2's receipt-only writer must carry these fields; the read-only adapter
	// must expose them without replacing them with receipt prose.
	newest := index.CheckpointRecord{Prev: &previous, Summary: old.Summary, SummaryRun: old.SummaryRun, CitedUsers: old.CitedUsers, Through: 5, Chain: "five", Receipt: []index.ReceiptLine{{Kind: "assistant", From: 5, Through: 5, Text: "decision"}}}
	source := recordSource(t, map[runstore.Hash]any{"latest": newest, previous: old, "user": &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "task"}}}}, map[runstore.Hash][]byte{summary: []byte("summary [message 1]"), "answer": []byte("answer")})
	in, err := FromRecord(t.Context(), source, "latest", entries, 99)
	require.NoError(t, err)
	assert.Equal(t, 99, in.CurrentPos, "K18 retains the recorded continuation position")
	assert.Equal(t, CheckpointID("latest"), in.ID)
	require.NotNil(t, in.Summary)
	assert.Equal(t, "summary [message 1]", in.Summary.Text)
	assert.Equal(t, "run", in.Summary.Run)
	assert.Equal(t, []UserLine{{ID: 1, Text: "task", Bytes: 4}}, in.CitedUsers)
	require.Len(t, in.Users, 2)
	assert.Equal(t, []index.MessageID{1, 4}, []index.MessageID{in.Users[0].ID, in.Users[1].ID})
	assert.Equal(t, "answer", in.Users[1].Message.Parts[0].(model.TextPart).Text)
	var receipt []ReceiptItem
	for item := range in.Receipt {
		receipt = append(receipt, item)
	}
	require.Len(t, receipt, 2)
	assert.Equal(t, "decision", receipt[0].Text)
	assert.Equal(t, "call", receipt[1].ToolUseID)
	assert.Equal(t, entries[2].ToolUseIDs[0], receipt[1].ToolUseID, "every covered result's call remains discoverable")
	assert.Equal(t, index.MessageID(5), receipt[0].AssistantID)
	assert.Equal(t, []ByteRange{{From: 1, Through: 9}}, receipt[1].Unsummarised)
	var repeated []ReceiptItem
	for item := range in.Receipt {
		repeated = append(repeated, item)
	}
	assert.Equal(t, receipt, repeated)
	_, err = Render(in, index.RenderBudget{KeepUserTokens: 500, MaxSummaryTokens: 500, ReceiptTokens: 500, FramingTokens: 200}, DefaultLimits, nil)
	assert.NoError(t, err)
}

func TestFromRecordFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		records map[runstore.Hash]any
		entries []index.MessageEntry
	}{
		{name: "missing", records: map[runstore.Hash]any{}},
		{name: "chain mismatch", records: map[runstore.Hash]any{"latest": index.CheckpointRecord{Through: 1, Chain: "wrong"}}, entries: []index.MessageEntry{{ID: 1, Chain: "chain"}}},
		{name: "cycle", records: map[runstore.Hash]any{"latest": index.CheckpointRecord{Prev: hashPointer("latest")}}},
		{name: "backwards", records: map[runstore.Hash]any{"latest": index.CheckpointRecord{Prev: hashPointer("old")}, "old": index.CheckpointRecord{Through: 1}}},
		{name: "bad receipt", records: map[runstore.Hash]any{"latest": index.CheckpointRecord{Receipt: []index.ReceiptLine{{Kind: "invalid"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FromRecord(t.Context(), recordSource(t, tc.records, nil), "latest", tc.entries, 0)
			assert.Error(t, err)
		})
	}
}
func hashPointer(hash runstore.Hash) *runstore.Hash { return &hash }
func recordSource(t *testing.T, values map[runstore.Hash]any, raw map[runstore.Hash][]byte) index.Source {
	t.Helper()
	data := make(map[runstore.Hash][]byte)
	for hash, value := range values {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		data[hash] = encoded
	}
	for hash, value := range raw {
		data[hash] = value
	}
	return func(_ context.Context, hash runstore.Hash) ([]byte, error) {
		value, ok := data[hash]
		if !ok {
			return nil, errors.New("missing content")
		}
		return value, nil
	}
}

func TestFromRecordMissingAndMalformedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record index.CheckpointRecord
		entry  index.MessageEntry
		raw    map[runstore.Hash][]byte
	}{
		{name: "missing summary", record: index.CheckpointRecord{Summary: hashPointer("summary")}},
		{name: "missing user", record: index.CheckpointRecord{Through: 1, Chain: "one"}, entry: index.MessageEntry{ID: 1, Kind: index.Message, Role: model.ConversationRoleUser, Hash: "user", Chain: "one"}},
		{name: "malformed user", record: index.CheckpointRecord{Through: 1, Chain: "one"}, entry: index.MessageEntry{ID: 1, Kind: index.Message, Role: model.ConversationRoleUser, Hash: "user", Chain: "one"}, raw: map[runstore.Hash][]byte{"user": []byte("{")}},
		{name: "non-user", record: index.CheckpointRecord{Through: 1, Chain: "one"}, entry: index.MessageEntry{ID: 1, Kind: index.Message, Role: model.ConversationRoleUser, Hash: "user", Chain: "one"}, raw: map[runstore.Hash][]byte{"user": []byte(`{"Role":"assistant","Parts":[]}`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FromRecord(t.Context(), recordSource(t, map[runstore.Hash]any{"latest": tc.record}, tc.raw), "latest", []index.MessageEntry{tc.entry}, 0)
			assert.Error(t, err)
		})
	}
	_, err := FromRecord(t.Context(), recordSource(t, nil, map[runstore.Hash][]byte{"latest": []byte(`{"unexpected":true}`)}), "latest", nil, 0)
	assert.Error(t, err)
}

func TestUnsafeEvictDropsSummaryRejected(t *testing.T) {
	previous, summary := runstore.Hash("previous"), runstore.Hash("summary")
	entries := []index.MessageEntry{{ID: 1, Chain: "one"}, {ID: 2, Chain: "two"}}
	old := index.CheckpointRecord{Summary: &summary, SummaryRun: "run", CitedUsers: []index.IndexLine{{ID: 1, Text: "instruction"}}, Through: 1, Chain: "one"}
	for _, field := range []string{"summary", "summary_run", "cited_users"} {
		t.Run(field, func(t *testing.T) {
			unsafe := index.CheckpointRecord{Prev: &previous, Summary: old.Summary, SummaryRun: old.SummaryRun, CitedUsers: old.CitedUsers, Through: 2, Chain: "two"}
			switch field {
			case "summary":
				unsafe.Summary = nil
			case "summary_run":
				unsafe.SummaryRun = ""
			case "cited_users":
				unsafe.CitedUsers = nil
			}
			_, err := FromRecord(t.Context(), recordSource(t, map[runstore.Hash]any{"latest": unsafe, previous: old}, map[runstore.Hash][]byte{summary: []byte("summary")}), "latest", entries, 0)
			assert.Error(t, err)
		})
	}
}

func TestFromRecordRecallLookup(t *testing.T) {
	record := index.CheckpointRecord{Receipt: []index.ReceiptLine{{Kind: "call", ToolUseID: "toolu_lookup", Tool: "runtime.recall_context", Text: `{"list":{"handle":"rc-deadbeef"}}`}}}
	in, err := FromRecord(t.Context(), recordSource(t, map[runstore.Hash]any{"latest": record}, nil), "latest", nil, 0)
	require.NoError(t, err)
	for item := range in.Receipt {
		assert.True(t, item.Lookup)
		assert.Equal(t, "runtime.recall_context", item.Tool, "record keeps the full identifier")
	}
	rendered, err := Render(in, index.RenderBudget{KeepUserTokens: 500, MaxSummaryTokens: 500, ReceiptTokens: 500, FramingTokens: 200}, DefaultLimits, nil)
	require.NoError(t, err)
	assert.Contains(t, rendered.Parts[0].(model.TextPart).Text, "context lookup:")
	assert.NotContains(t, rendered.Parts[0].(model.TextPart).Text, "recall_context")
}
