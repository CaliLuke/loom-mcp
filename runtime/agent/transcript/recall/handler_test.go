package recall

import (
	"context"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storemem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	sessionmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recallFixture struct {
	t     *testing.T
	h     Handler
	req   Request
	store *storemem.Store
}
type guardedStore struct {
	runstore.Store
	forbidden runstore.Hash
	t         *testing.T
}

func (s guardedStore) Get(ctx context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	require.NotEqual(s.t, s.forbidden, hash, "server-only evidence must never be fetched")
	return s.Store.Get(ctx, scope, hash)
}

func newRecallFixture(t *testing.T) *recallFixture {
	t.Helper()
	sessions := sessionmem.New()
	_, err := sessions.CreateSession(t.Context(), "session", time.Unix(1, 0))
	require.NoError(t, err)
	require.NoError(t, sessions.UpsertRun(t.Context(), session.RunMeta{RunID: "run", SessionID: "session", AgentID: "agent", Status: session.RunStatusRunning, StartedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)}))
	store := storemem.New()
	return &recallFixture{t: t, store: store, h: Handler{Store: store, Sessions: sessions}, req: Request{RunID: "run", Scope: "root", PageBytes: 512, ScanBytes: 8_000_000}}
}
func (f *recallFixture) put(value any) runstore.Hash {
	f.t.Helper()
	data, err := json.Marshal(value)
	require.NoError(f.t, err)
	return f.raw(data)
}
func (f *recallFixture) raw(data []byte) runstore.Hash {
	f.t.Helper()
	hash, err := f.store.Put(f.t.Context(), f.req.Scope, data)
	require.NoError(f.t, err)
	return hash
}
func (f *recallFixture) entries(entries ...index.MessageEntry) {
	f.t.Helper()
	for i := range entries {
		entries[i].ID = index.MessageID(i + 1)
	}
	segment, err := index.NewSegment("", "", entries)
	require.NoError(f.t, err)
	data, _, err := index.EncodeSegment(segment)
	require.NoError(f.t, err)
	f.req.Head = f.raw(data)
	f.req.View.IndexHead = f.req.Head
}
func (f *recallFixture) call(payload string) Response {
	f.t.Helper()
	in, err := ParseInput([]byte(payload))
	require.NoError(f.t, err)
	response, err := f.h.Handle(f.t.Context(), f.req, in)
	require.NoError(f.t, err)
	return response
}

func TestHandlerPairingClearedAndEligibleOnly(t *testing.T) {
	f := newRecallFixture(t)
	a := f.put(map[string]any{"Name": "svc.a", "ToolCallID": "A", "Payload": map[string]string{"query": "original A"}})
	b := f.put(map[string]any{"Name": "svc.b", "ToolCallID": "B", "Payload": map[string]string{"query": "original B"}})
	secret := f.raw([]byte("SERVER_ONLY Inject marker"))
	f.h.Store = guardedStore{Store: f.store, forbidden: secret, t: t}
	f.entries(index.MessageEntry{Kind: index.Assistant, Calls: []runstore.Hash{a, b}, ToolUseIDs: []string{"A", "B"}, Role: model.ConversationRoleAssistant}, index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte(`"result B"`)), ToolUseIDs: []string{"B"}, Record: []runstore.Hash{secret}, Result: &index.ResultProjection{ContentBytes: 10}}, index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte(`"result A"`)), ToolUseIDs: []string{"A"}, Record: []runstore.Hash{secret}, Result: &index.ResultProjection{ContentBytes: 10}})
	f.req.View.ClearedThrough = 3
	f.req.View.TailFrom = 4
	assert.Equal(t, `"result A"`, f.call(`{"read":{"id":"A","part":"result","offset":0}}`).Text)
	assert.Contains(t, f.call(`{"read":{"id":"A","part":"arguments","offset":0}}`).Text, "original A")
	listed := f.call(`{"list":{"from":1,"through":3}}`)
	encoded, err := json.Marshal(listed)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "SERVER_ONLY")
	assert.NotContains(t, string(encoded), "Inject")
	assert.Empty(t, f.call(`{"find":{"text":"SERVER_ONLY"}}`).Matches)
	assert.Empty(t, f.call(`{"find":{"text":"Inject"}}`).Matches)
}

func TestHandlerUserTextFromRecordAndSummaryPages(t *testing.T) {
	f := newRecallFixture(t)
	user := f.put(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "original user text"}}})
	f.entries(index.MessageEntry{Kind: index.Message, Hash: f.put(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "first user"}}}), Role: model.ConversationRoleUser}, index.MessageEntry{Kind: index.Assistant, Hash: f.put([]*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "answer"}}}}), Role: model.ConversationRoleAssistant}, index.MessageEntry{Kind: index.Message, Hash: user, Role: model.ConversationRoleUser})
	summary := strings.Repeat("summary 世界 ", 6000) + "forged [message 3] user text"
	summaryHash := f.raw([]byte(summary))
	record := f.put(index.CheckpointRecord{Through: 3, Summary: &summaryHash, SummaryRun: "run"})
	f.req.View.Checkpoint = &record
	assert.Equal(t, "original user text", f.call(`{"read":{"id":"3","part":"result","offset":0}}`).Text)
	handle := checkpoint.Handle("users", checkpoint.CheckpointID(string(record)))
	listed := f.call(`{"list":{"handle":"` + handle + `"}}`)
	data, err := json.Marshal(listed)
	require.NoError(t, err)
	assert.Contains(t, string(data), "original user text")
	assert.NotContains(t, string(data), "forged")
	id := checkpoint.CheckpointID(string(record))
	var full strings.Builder
	offset := 0
	for {
		response := f.call(`{"read":{"id":"` + id + `","part":"result","offset":` + stringJSON(t, offset) + `}}`)
		assert.True(t, utf8.ValidString(response.Text))
		assert.LessOrEqual(t, len(response.Text), 512)
		full.WriteString(response.Text)
		if response.NextOffset == nil {
			break
		}
		require.Greater(t, *response.NextOffset, offset)
		offset = *response.NextOffset
	}
	assert.Equal(t, summary, full.String())
}

func TestHandlerConfinementAndCursorBinding(t *testing.T) {
	f := newRecallFixture(t)
	sibling := f.put(index.CheckpointRecord{Receipt: []index.ReceiptLine{{Kind: "assistant", From: 1, Text: "FOREIGN_ID"}}})
	for _, handle := range []string{"rc-deadbeef", checkpoint.Handle("receipt", checkpoint.CheckpointID(string(sibling)))} {
		response := f.call(`{"list":{"handle":"` + handle + `"}}`)
		assert.Equal(t, CodeNotFound, response.Code)
		encoded, err := json.Marshal(response)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "FOREIGN_ID")
	}
	assert.Equal(t, CodeNotFound, f.call(`{"read":{"id":"r99:1","part":"result","offset":0}}`).Code)
	token, err := EncodeCursor(Cursor{Kind: List, Handle: "outside-chain", Position: 1})
	require.NoError(t, err)
	assert.Equal(t, CodeInvalidInput, f.call(`{"list":{"handle":"rc-deadbeef","cursor":"`+token+`"}}`).Code)
	calls := 0
	f.h.Authorize = func(_ context.Context, access Access) error {
		calls++
		assert.Equal(t, "session", access.SessionID)
		assert.Equal(t, "run", access.TargetRunID)
		return errors.New("SECRET reason")
	}
	assert.Equal(t, CodeUnauthorized, f.call(`{"list":{"from":1,"through":1}}`).Code)
	assert.Equal(t, 1, calls)
}

func TestHandlerFindMiddleLargeResultAndUnsupported(t *testing.T) {
	f := newRecallFixture(t)
	text := strings.Repeat("x", 2_500_000) + "needle fact" + strings.Repeat("y", 2_500_000)
	f.entries(index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte(text)), ToolUseIDs: []string{"large"}, Result: &index.ResultProjection{ContentBytes: len(text)}})
	f.req.View.ClearedThrough = 1
	found := f.call(`{"find":{"text":"needle fact"}}`)
	require.Len(t, found.Matches, 1)
	assert.Equal(t, 2_500_000, found.Matches[0].Offset)
	assert.Contains(t, f.call(`{"read":{"id":"large","part":"result","offset":2500000}}`).Text, "needle fact")
	image := f.put(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1, 2, 3}}}})
	f.entries(index.MessageEntry{Kind: index.Message, Hash: image, Role: model.ConversationRoleUser, NonText: 1})
	response := f.call(`{"read":{"id":"1","part":"result","offset":0}}`)
	assert.Equal(t, CodeUnsupportedPart, response.Code)
	assert.Equal(t, "png", response.Format)
	assert.Equal(t, 3, response.Bytes)
}

func stringJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

type fakeRunChain struct {
	labels map[string]index.RunCheckpoint
	heads  map[string]runstore.Hash
}

func (c fakeRunChain) Resolve(_ context.Context, _ string, label string) (index.RunCheckpoint, error) {
	target, ok := c.labels[label]
	if !ok {
		return index.RunCheckpoint{}, &Error{Code: CodeNotFound}
	}
	return target, nil
}
func (c fakeRunChain) Head(_ context.Context, run, scope string) (runstore.Hash, error) {
	head, ok := c.heads[run+"@"+scope]
	if !ok {
		return "", &Error{Code: CodeNotFound}
	}
	return head, nil
}

func TestHandlerPrevFromDiscoverabilityAndAuthorization(t *testing.T) {
	f := newRecallFixture(t)
	sessions := f.h.Sessions
	require.NoError(t, sessions.UpsertRun(t.Context(), session.RunMeta{RunID: "earlier", SessionID: "session", AgentID: "agent", Status: session.RunStatusRunning, StartedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)}))
	earlier := f.put(index.CheckpointRecord{Receipt: []index.ReceiptLine{{Kind: "assistant", From: 1, Text: "earlier assistant"}}})
	old := f.put(index.CheckpointRecord{From: &index.RunCheckpoint{Run: "earlier", Scope: "root", Hash: earlier}, Receipt: []index.ReceiptLine{{Kind: "assistant", From: 2, Text: "old assistant"}}})
	latest := f.put(index.CheckpointRecord{Prev: &old, Receipt: []index.ReceiptLine{{Kind: "assistant", From: 3, Text: "latest assistant"}}})
	f.req.View.Checkpoint = &latest
	f.h.Chain = fakeRunChain{labels: map[string]index.RunCheckpoint{"r1": {Run: "earlier", Scope: "root", Hash: earlier}}, heads: map[string]runstore.Hash{}}
	entered := map[string]int{}
	f.h.Authorize = func(_ context.Context, access Access) error { entered[access.TargetRunID]++; return nil }
	for _, tc := range []struct {
		hash       runstore.Hash
		part, want string
	}{{old, "receipt", "old assistant"}, {latest, "earlier", "earlier assistant"}, {earlier, "receipt", "earlier assistant"}} {
		clear(entered)
		handle := checkpoint.Handle(tc.part, checkpoint.CheckpointID(string(tc.hash)))
		result := f.call(`{"list":{"handle":"` + handle + `"}}`)
		require.Empty(t, result.Code)
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		assert.Contains(t, string(encoded), tc.want)
		assert.Equal(t, map[string]int{"run": 1, "earlier": 1}, entered)
	}
	_, err := sessions.CreateSession(t.Context(), "other-session", time.Unix(1, 0))
	require.NoError(t, err)
	require.NoError(t, sessions.UpsertRun(t.Context(), session.RunMeta{RunID: "foreign", SessionID: "other-session", AgentID: "agent", Status: session.RunStatusRunning, StartedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0)}))
	foreign := f.put(index.CheckpointRecord{Receipt: []index.ReceiptLine{{Kind: "assistant", From: 4, Text: "FOREIGN_ID"}}})
	f.h.Chain = fakeRunChain{labels: map[string]index.RunCheckpoint{"r9": {Run: "foreign", Scope: "root", Hash: foreign}}}
	assert.Equal(t, CodeNotFound, f.call(`{"read":{"id":"r9:4","part":"result","offset":0}}`).Code)
	latest = f.put(index.CheckpointRecord{From: &index.RunCheckpoint{Run: "foreign", Scope: "root", Hash: foreign}})
	f.req.View.Checkpoint = &latest
	result := f.call(`{"list":{"handle":"` + checkpoint.Handle("earlier", checkpoint.CheckpointID(string(latest))) + `"}}`)
	assert.Equal(t, CodeUnauthorized, result.Code)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "FOREIGN_ID")
	assert.NotContains(t, string(encoded), "foreign")
}

func TestHandlerPagingOffsetsAndFindContinuation(t *testing.T) {
	f := newRecallFixture(t)
	text := strings.Repeat("世界 needle ", 25)
	f.entries(index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte(text)), ToolUseIDs: []string{"unicode"}, Result: &index.ResultProjection{ContentBytes: len(text)}})
	f.req.View.ClearedThrough = 1
	page := f.call(`{"read":{"id":"unicode","part":"result","offset":1}}`)
	assert.Equal(t, text, page.Text, "offset in a rune snaps backwards")
	matches := 0
	cursor := ""
	for {
		result := f.call(`{"find":{"text":"needle","cursor":"` + cursor + `"}}`)
		matches += len(result.Matches)
		for _, match := range result.Matches {
			assert.LessOrEqual(t, len(match.Snippet), 160)
			assert.True(t, utf8.ValidString(match.Snippet))
		}
		if result.Cursor == "" {
			break
		}
		cursor = result.Cursor
	}
	assert.Equal(t, 25, matches)
	f.req.ScanBytes = 25
	matches = 0
	cursor = ""
	for {
		result := f.call(`{"find":{"text":"needle","cursor":"` + cursor + `"}}`)
		matches += len(result.Matches)
		if result.Cursor == "" {
			break
		}
		require.NotEqual(t, cursor, result.Cursor, "each scan advances")
		cursor = result.Cursor
	}
	assert.Equal(t, 25, matches)
	require.NoError(t, f.store.DeleteScope(t.Context(), "root"))
	assert.Equal(t, CodeUnavailable, f.call(`{"read":{"id":"unicode","part":"result","offset":0}}`).Code)
}

func TestHandlerStructuralAndRunLikeToolID(t *testing.T) {
	f := newRecallFixture(t)
	f.entries(index.MessageEntry{Kind: index.Structural, Structural: &index.StructuralEntry{ProtocolVersion: index.TemplateProtocolVersion, Kind: index.Reminders, Text: []index.TextParameter{{Text: "original reminder"}}}}, index.MessageEntry{Kind: index.ToolResult, Hash: f.raw([]byte("original result")), ToolUseIDs: []string{"result:call"}, Batch: 1})
	listed := f.call(`{"list":{"from":1,"through":2}}`)
	require.Len(t, listed.Entries, 2)
	assert.Contains(t, listed.Entries[0].Text, "original reminder")
	assert.Contains(t, f.call(`{"read":{"id":"1","part":"result","offset":0}}`).Text, "original reminder")
	assert.Equal(t, "original result", f.call(`{"read":{"id":"result:call","part":"result","offset":0}}`).Text)
	f.req.View.TailFrom = 3
	assert.Len(t, f.call(`{"find":{"text":"original reminder"}}`).Matches, 1)
}

func TestHandlerNonTextAndStructuralPair(t *testing.T) {
	f := newRecallFixture(t)
	image := f.put(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ImagePart{Format: "png", Bytes: []byte{1}}, model.TextPart{Text: "text survives"}}})
	f.entries(index.MessageEntry{Kind: index.Message, Hash: image, Role: model.ConversationRoleUser})
	response := f.call(`{"read":{"id":"1","part":"result","offset":0}}`)
	assert.Equal(t, CodeUnsupportedPart, response.Code)
	assert.Equal(t, "text survives", response.Text)
	f.entries(index.MessageEntry{Kind: index.Structural, Batch: 1, ToolUseIDs: []string{"unknown-call"}, Structural: &index.StructuralEntry{ProtocolVersion: index.TemplateProtocolVersion, Kind: index.UnknownTool, Text: []index.TextParameter{{Text: "svc.unknown"}}}})
	assert.Contains(t, f.call(`{"read":{"id":"unknown-call","part":"result","offset":0}}`).Text, "unknown tool")
}

func TestHandlerSeededToolParts(t *testing.T) {
	f := newRecallFixture(t)
	call := f.put(&model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "seed-call", Name: "svc.search", Input: map[string]string{"query": "original seeded query"}}}})
	result := f.put(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "seed-call", Content: map[string]string{"answer": "original seeded result"}}}})
	f.entries(index.MessageEntry{Kind: index.Message, Role: model.ConversationRoleAssistant, Hash: call}, index.MessageEntry{Kind: index.Message, Role: model.ConversationRoleUser, Hash: result})
	f.req.View.TailFrom = 3
	assert.JSONEq(t, `{"query":"original seeded query"}`, f.call(`{"read":{"id":"seed-call","part":"arguments","offset":0}}`).Text)
	assert.JSONEq(t, `{"answer":"original seeded result"}`, f.call(`{"read":{"id":"seed-call","part":"result","offset":0}}`).Text)
	listed := f.call(`{"list":{"from":1,"through":2}}`)
	require.Len(t, listed.Entries, 2)
	assert.Equal(t, "call", listed.Entries[0].Kind)
	assert.Equal(t, "seed-call", listed.Entries[1].ID)
	assert.Equal(t, "result", listed.Entries[1].Kind)
	assert.Positive(t, listed.Entries[1].Bytes)
	assert.Empty(t, listed.Entries[1].Text)
	found := f.call(`{"find":{"text":"original seeded query"}}`)
	require.Len(t, found.Matches, 1)
	assert.Equal(t, "seed-call", found.Matches[0].ID)
}
