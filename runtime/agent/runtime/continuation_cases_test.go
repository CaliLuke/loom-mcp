package runtime

import (
	"context"
	"encoding/json/v2"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type continuationContentProbe struct {
	runstore.Store
	missingScope string
	missingHash  runstore.Hash
	copiedSecret bool
}

func TestContinuationOverlapFreshMissingAndFailure(t *testing.T) {
	rt := New()
	_, err := rt.SessionStore.CreateSession(t.Context(), "session", time.Now())
	require.NoError(t, err)
	message := func(text string) *model.Message {
		return &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: text}}}
	}
	a := continuationTestStart(t, rt, "a", []*model.Message{message("original")})
	b := continuationTestStart(t, rt, "b", []*model.Message{message("overlap")})
	continuationTestSeal(t, rt, "a", a, true)
	continuationTestSeal(t, rt, "b", b, true)
	head, err := rt.SessionStore.(session.ContinuationStore).LoadContinuationHead(t.Context(), "session")
	require.NoError(t, err)
	assert.Equal(t, "a", head.ID)
	superseded, err := rt.continuationEvent(t.Context(), "b", "continuation_superseded")
	require.NoError(t, err)
	require.NotNil(t, superseded)
	c := continuationTestStart(t, rt, "c", []*model.Message{message("different")})
	assert.Equal(t, "no_match", c.Continuation.Reason)
	assert.Equal(t, 2, c.Continuation.Pos)
	continuationTestSeal(t, rt, "c", c, true)
	d := continuationTestStart(t, rt, "d", []*model.Message{message("failure")})
	continuationTestSeal(t, rt, "d", d, false)
	head, err = rt.SessionStore.(session.ContinuationStore).LoadContinuationHead(t.Context(), "session")
	require.NoError(t, err)
	assert.Equal(t, "c", head.ID)
	rt.RunStore = &continuationContentProbe{Store: rt.RunStore, missingScope: "c", missingHash: *head.Tail[0].Bytes}
	missing := continuationTestStart(t, rt, "missing", []*model.Message{message("different"), {Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "final"}}}})
	assert.Equal(t, "missing_content", missing.Continuation.Reason)
	assert.Equal(t, 3, missing.Continuation.Pos)
}

func TestContinuationSealBodyConflict(t *testing.T) {
	rt := New()
	_, err := rt.SessionStore.CreateSession(t.Context(), "session", time.Now())
	require.NoError(t, err)
	start := continuationTestStart(t, rt, "a", []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}})
	in := continuationTestSeal(t, rt, "a", start, true)
	in.Pos++
	setContinuationSealView(in, start.Continuation.View)
	_, err = rt.SealRecordActivity(t.Context(), in)
	assert.ErrorContains(t, err, "conflict")
}

func TestContinuationCopyExcludesHugeKeptResult(t *testing.T) {
	rt := New()
	_, err := rt.SessionStore.CreateSession(t.Context(), "session", time.Now())
	require.NoError(t, err)
	messages := make([]*model.Message, 0, 8)
	messages = append(messages, []*model.Message{
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "find evidence"}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "huge-call", Name: "tool", Input: map[string]string{"query": strings.Repeat("argument ", 30000)}}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "huge-call", Content: strings.Repeat("SECRET_RESULT", 80000)}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: strings.Repeat("conclusion ", 30000)}}},
	}...)
	first := continuationTestStart(t, rt, "r1", messages)
	// Seeded messages can include a kept result just as runtime tool entries do.
	first.Entries[2].Keep = true
	head, committed, err := storeIndexSegments(t.Context(), rt.RunStore, "r1", "", "", first.Entries)
	require.NoError(t, err)
	first.IndexHead, first.Entries = head, committed
	continuationTestSeal(t, rt, "r1", first, true)
	probe := &continuationContentProbe{Store: rt.RunStore}
	rt.RunStore = probe
	final := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "final"}}}
	messages = append(messages, final, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "next question"}}})
	second := continuationTestStart(t, rt, "r2", messages)
	require.Equal(t, "continued", second.Continuation.Outcome)
	assert.False(t, probe.copiedSecret)
	assert.NotNil(t, second.Continuation.View.Checkpoint)

	continuationTestSeal(t, rt, "r2", second, true)
	messages = append(messages, final, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "next question"}}})
	third := continuationTestStart(t, rt, "r3", messages)
	require.Equal(t, "continued", third.Continuation.Outcome)
	loaded, err := rt.loadView(t.Context(), "r3", "r3", third.Continuation.View)
	require.NoError(t, err)
	encoded, err := json.Marshal(loaded.Messages)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "SECRET_RESULT")

	payload := rawjson.Message(`{"read":{"run":"r1","id":"huge-call","part":"result","offset":0}}`)
	out, err := rt.RecallActivity(t.Context(), &api.RecallActivityInput{RunID: "r3", Scope: "r3", ViewRef: third.Continuation.View, Head: third.IndexHead, Call: planner.ToolRequest{Payload: payload}})
	require.NoError(t, err)
	data, err := rt.RunStore.Get(t.Context(), "r3", out.Result.Refs.Result)
	require.NoError(t, err)
	assert.Contains(t, string(data), "SECRET_RESULT")
}
func (p *continuationContentProbe) Get(ctx context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	if scope == p.missingScope && hash == p.missingHash {
		return nil, runstore.ErrNotFound
	}
	return p.Store.Get(ctx, scope, hash)
}
func (p *continuationContentProbe) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	if scope == "r2" && strings.Contains(string(data), "SECRET_RESULT") {
		p.copiedSecret = true
	}
	return p.Store.Put(ctx, scope, data)
}

func continuationTestStart(t *testing.T, rt *Runtime, id string, messages []*model.Message) *api.RecordContentOutput {
	t.Helper()
	require.NoError(t, rt.SessionStore.UpsertRun(t.Context(), session.RunMeta{RunID: id, SessionID: "session", AgentID: "a", Status: session.RunStatusRunning}))
	items, err := encodeContentMessages(messages)
	require.NoError(t, err)
	out, err := rt.RecordContentActivity(t.Context(), &api.RecordContentInput{RootRunID: id, Items: items, SeedMessages: true, Continue: &api.ContinuationStartInput{SessionID: "session", RunID: id, AgentID: "a", Timestamp: time.Unix(1, 0)}})
	require.NoError(t, err)
	return out
}

func continuationTestSeal(t *testing.T, rt *Runtime, id string, start *api.RecordContentOutput, publish bool) *api.SealRecordInput {
	t.Helper()
	data, err := json.Marshal(&model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "final"}}})
	require.NoError(t, err)
	hash, err := rt.RunStore.Put(t.Context(), id, data)
	require.NoError(t, err)
	entry := index.MessageEntry{ID: index.MessageID(len(start.Entries) + 1), Kind: index.Message, Hash: hash, Role: model.ConversationRoleAssistant, Bytes: 5}
	var prev runstore.Hash
	if len(start.Entries) > 0 {
		prev = start.Entries[len(start.Entries)-1].Chain
	}
	entry.Chain, err = index.ComputeChain(prev, entry)
	require.NoError(t, err)
	in := &api.SealRecordInput{Pending: []index.MessageEntry{entry}, RootRunID: id, RunID: id, ProtocolVersion: api.WorkflowProtocolVersion, IndexHead: start.IndexHead, EventKey: "run_sealed:" + id, Timestamp: time.Unix(2, 0), SessionID: "session", Expected: start.Continuation.Prev, Pos: start.Continuation.Pos, FinalHash: hash, Publish: publish, ContextCompaction: &ContextCompaction{}}
	setContinuationSealView(in, start.Continuation.View)
	_, err = rt.SealRecordActivity(t.Context(), in)
	require.NoError(t, err)
	return in
}
