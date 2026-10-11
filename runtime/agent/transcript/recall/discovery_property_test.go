package recall

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecallDiscoverableKindSequences enumerates all kind sequences through six,
// every retained prefix of zero to two receipt lines, and one to three Prev
// records with an injected From. Users resolve from rendered handles or IDs to
// original text. Parallel results consume B before A.
func TestRecallDiscoverableKindSequences(t *testing.T) {
	cases := 0
	for n := 0; n <= 6; n++ {
		for bits := 0; bits < 1<<(2*n); bits++ {
			f := newRecallFixture(t)
			var entries []index.MessageEntry
			var receipt []index.ReceiptLine
			var users []checkpoint.UserMessage
			var pending []string
			expected := map[string]string{}
			valid := true
			for i := 0; i < n; i++ {
				id := index.MessageID(i + 1)
				switch bits >> (2 * i) & 3 {
				case 0:
					message := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: fmt.Sprintf("user %d original\nuser %d full evidence", id, id)}}}
					users = append(users, checkpoint.UserMessage{ID: id, Message: message})
					entries = append(entries, index.MessageEntry{Kind: index.Message, Role: model.ConversationRoleUser, Hash: f.put(message)})
				case 1:
					entries = append(entries, index.MessageEntry{Kind: index.Assistant, Role: model.ConversationRoleAssistant, Hash: f.put([]*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "assistant original"}}}})})
					receipt = append(receipt, index.ReceiptLine{Kind: "assistant", From: id, Through: id, Text: "assistant original"})
				case 2:
					var hashes []runstore.Hash
					ids := []string{fmt.Sprintf("toolu_%d_A", i), fmt.Sprintf("toolu_%d_B", i)}
					for _, callID := range ids {
						hashes = append(hashes, f.put(map[string]any{"Name": "svc.search", "ToolCallID": callID, "Payload": map[string]string{"query": callID}}))
						receipt = append(receipt, index.ReceiptLine{Kind: "call", From: id, Through: id, Tool: "svc.search", ToolUseID: callID, Text: "args"})
					}
					pending = append(pending, ids...)
					entries = append(entries, index.MessageEntry{Kind: index.Assistant, Calls: hashes, ToolUseIDs: ids})
				case 3:
					if len(pending) == 0 {
						valid = false
						break
					}
					callID := pending[len(pending)-1]
					pending = pending[:len(pending)-1]
					text := "result for " + callID
					entries = append(entries, index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte(text)), ToolUseIDs: []string{callID}, Batch: id, Result: &index.ResultProjection{ContentBytes: len(text)}})
					expected[callID] = text
				}
				if !valid {
					break
				}
			}
			if !valid {
				continue
			}
			if len(entries) > 0 {
				f.entries(entries...)
			}
			require.NoError(t, f.h.Sessions.UpsertRun(t.Context(), session.RunMeta{RunID: "prior", SessionID: "session", AgentID: "agent", Status: session.RunStatusRunning, StartedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)}))
			from := f.put(index.CheckpointRecord{Receipt: []index.ReceiptLine{{Kind: "assistant", From: 1, Through: 1, Text: "prior assistant"}}})
			for depth := 1; depth <= 3; depth++ {
				var previous *runstore.Hash
				var roots []runstore.Hash
				for part := 0; part < depth; part++ {
					lo, hi := len(receipt)*part/depth, len(receipt)*(part+1)/depth
					record := index.CheckpointRecord{Prev: previous, Receipt: receipt[lo:hi], Through: index.MessageID(n)}
					if part == 0 {
						record.From = &index.RunCheckpoint{Run: "prior", Scope: f.req.Scope, Hash: from}
					}
					hash := f.put(record)
					previous = &hash
					roots = append(roots, hash)
				}
				f.req.View.Checkpoint = previous
				f.req.PageBytes = 64_000
				for shown := 0; shown <= 2; shown++ {
					// A compact handle covers the entire omitted suffix, independently of
					// which newest lines fit. It must return all assistant and call lines.
					newest := roots[len(roots)-1]
					handle := checkpoint.Handle("receipt", checkpoint.CheckpointID(string(newest)))
					in := checkpoint.Input{ID: checkpoint.CheckpointID(string(newest)), Through: index.MessageID(n), Users: users, Earlier: checkpoint.EarlierCounts{Assistants: 1}}
					in.Receipt = func(yield func(checkpoint.ReceiptItem) bool) {
						for i := len(receipt) - 1; i >= 0; i-- {
							line := receipt[i]
							item := checkpoint.ReceiptItem{ToolUseID: line.ToolUseID, Tool: line.Tool, Args: strings.Repeat("args ", 50), AssistantID: line.From, Text: strings.Repeat(line.Text+" ", 30)}
							if line.Kind == "call" {
								item.AssistantID = 0
								item.Text = ""
							}
							if !yield(item) {
								return
							}
						}
					}
					rendered, err := checkpoint.Render(in, index.RenderBudget{ReceiptTokens: shown * 125, FramingTokens: 350}, checkpoint.DefaultLimits, nil)
					require.NoError(t, err)
					text := rendered.Parts[0].(model.TextPart).Text
					assertOriginalUsersDiscoverable(t, f, text, newest, users)
					if len(receipt) > shown {
						assert.Contains(t, text, handle)
					}
					listed := f.call(`{"list":{"handle":"` + handle + `"}}`)
					require.Len(t, listed.Entries, len(receipt))
					for i, line := range listed.Entries {
						original := receipt[len(receipt)-1-i]
						if original.Kind == "assistant" {
							assert.Equal(t, strconv.FormatUint(uint64(original.From), 10), line.ID)
							assert.Contains(t, line.Text, "assistant original")
						} else {
							assert.Equal(t, original.ToolUseID, line.ID)
						}
					}
					earlier := checkpoint.Handle("earlier", checkpoint.CheckpointID(string(newest)))
					prior := f.call(`{"list":{"handle":"` + earlier + `"}}`)
					require.Len(t, prior.Entries, 1)
					assert.Equal(t, "prior assistant", prior.Entries[0].Text)
					// Every older handle also resolves its suffix of the Prev chain.
					for at, hash := range roots {
						old := checkpoint.Handle("receipt", checkpoint.CheckpointID(string(hash)))
						response := f.call(`{"list":{"handle":"` + old + `"}}`)
						assert.Len(t, response.Entries, len(receipt)*(at+1)/depth)
					}
					cases++
				}
			}
			for callID, text := range expected {
				assert.Equal(t, text, f.call(`{"read":{"id":"`+callID+`","part":"result","offset":0}}`).Text)
			}
		}
	}
	t.Logf("Discoverable exhaustive cases: %d", cases)
}

// assertOriginalUsersDiscoverable follows the rendered discovery surface before
// checking that every listed user ID reads the full original, not just its index line.
func assertOriginalUsersDiscoverable(t *testing.T, f *recallFixture, rendered string, hash runstore.Hash, users []checkpoint.UserMessage) {
	t.Helper()
	handle := checkpoint.Handle("users", checkpoint.CheckpointID(string(hash)))
	if strings.Contains(rendered, handle) {
		listed := f.call(`{"list":{"handle":` + stringJSON(t, handle) + `}}`)
		require.Len(t, listed.Entries, len(users))
		for i, user := range users {
			assert.Equal(t, strconv.FormatUint(uint64(user.ID), 10), listed.Entries[i].ID)
			assert.Equal(t, "user", listed.Entries[i].Kind)
			assert.Equal(t, fmt.Sprintf("user %d original", user.ID), listed.Entries[i].Text)
		}
	} else {
		for _, user := range users {
			assert.Contains(t, rendered, checkpoint.Label(user.ID, nil, 0))
		}
	}
	for _, user := range users {
		read := f.call(stringJSON(t, Input{Read: &ReadInput{ID: strconv.FormatUint(uint64(user.ID), 10), Part: Result}}))
		assert.Empty(t, read.Code)
		assert.Equal(t, fmt.Sprintf("user %d original\nuser %d full evidence", user.ID, user.ID), read.Text)
	}
}
