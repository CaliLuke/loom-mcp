package recall

import (
	"context"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recallReadFault struct {
	runstore.Store
	reads  []runstore.Hash
	failAt int
}

func TestRecallStorageFailuresDoNotReturnPartialEvidence(t *testing.T) {
	f := newRecallFixture(t)
	user := f.put(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "remember staging"}}})
	call := f.put(map[string]any{"Name": "docs.read", "ToolCallID": "call", "Payload": map[string]string{"query": "staging"}})
	result := f.put(map[string]string{"answer": "staging uses version two"})
	f.entries(index.MessageEntry{Kind: index.Message, Role: model.ConversationRoleUser, Hash: user}, index.MessageEntry{Kind: index.Assistant, Calls: []runstore.Hash{call}, ToolUseIDs: []string{"call"}}, index.MessageEntry{Kind: index.ToolResult, Hash: result, ToolUseIDs: []string{"call"}, Batch: 3})
	summary := f.raw([]byte("staging uses version two [message 1]"))
	record := f.put(index.CheckpointRecord{Through: 3, Summary: &summary, SummaryRun: "run", Receipt: []index.ReceiptLine{{Kind: "call", From: 2, Through: 3, Tool: "docs.read", ToolUseID: "call", Text: "staging"}}})
	f.req.View.Checkpoint = &record
	for _, payload := range []string{
		`{"list":{"from":1,"through":3}}`,
		`{"read":{"id":"1","part":"result","offset":0}}`,
		`{"read":{"id":"call","part":"arguments","offset":0}}`,
		`{"read":{"id":"call","part":"result","offset":0}}`,
		`{"find":{"text":"staging"}}`,
		`{"read":{"id":"` + checkpoint.CheckpointID(string(record)) + `","part":"result","offset":0}}`,
		`{"list":{"handle":"` + checkpoint.Handle("users", checkpoint.CheckpointID(string(record))) + `"}}`,
		`{"list":{"handle":"` + checkpoint.Handle("receipt", checkpoint.CheckpointID(string(record))) + `"}}`,
	} {
		t.Run(payload, func(t *testing.T) {
			input, err := ParseInput([]byte(payload))
			require.NoError(t, err)
			trace := &recallReadFault{Store: f.store}
			handler := f.h
			handler.Store = trace
			baseline, err := handler.Handle(t.Context(), f.req, input)
			require.NoError(t, err)
			require.Empty(t, baseline.Code)
			require.NotEmpty(t, trace.reads)
			for i := range trace.reads {
				t.Run(fmt.Sprintf("read_%d", i+1), func(t *testing.T) {
					fault := &recallReadFault{Store: f.store, failAt: i + 1}
					handler := f.h
					handler.Store = fault
					out, err := handler.Handle(t.Context(), f.req, input)
					require.ErrorIs(t, err, assert.AnError)
					assert.GreaterOrEqual(t, len(fault.reads), i+1)
					assert.Empty(t, out.Text)
					assert.Empty(t, out.Entries)
					assert.Empty(t, out.Matches)
				})
			}
		})
	}
}

func (s *recallReadFault) Get(ctx context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	s.reads = append(s.reads, hash)
	if len(s.reads) == s.failAt {
		return nil, assert.AnError
	}
	return s.Store.Get(ctx, scope, hash)
}
