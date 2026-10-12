package runtime

import (
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContinuationSealSeedHistory(t *testing.T) {
	rt := New()
	_, err := rt.SessionStore.CreateSession(t.Context(), "session", time.Now())
	require.NoError(t, err)
	user := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "remember the decision"}}}
	final := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "choose blue after last compaction"}}}
	items, err := encodeContentMessages([]*model.Message{user, final})
	require.NoError(t, err)
	seed, err := rt.RecordContentActivity(t.Context(), &api.RecordContentInput{RootRunID: "r1", Items: items[:1], SeedMessages: true, Continue: &api.ContinuationStartInput{SessionID: "session", RunID: "r1", AgentID: "a", Timestamp: time.Unix(1, 0)}})
	require.NoError(t, err)
	require.NotNil(t, seed.Continuation)
	assert.Equal(t, 1, seed.Continuation.Pos)
	assert.Equal(t, "no_head", seed.Continuation.Reason)
	finalHash, err := rt.RunStore.Put(t.Context(), "r1", items[1])
	require.NoError(t, err)
	pending := index.MessageEntry{ID: 2, Kind: index.Message, Hash: finalHash, Role: model.ConversationRoleAssistant, Bytes: 32}
	pending.Chain, err = index.ComputeChain(seed.Entries[0].Chain, pending)
	require.NoError(t, err)
	_, err = rt.SealRecordActivity(t.Context(), &api.SealRecordInput{RootRunID: "r1", RunID: "r1", ProtocolVersion: api.WorkflowProtocolVersion, IndexHead: seed.IndexHead, Pending: []index.MessageEntry{pending}, EventKey: "run_sealed:r1", Timestamp: time.Unix(2, 0), SessionID: "session", Pos: 1, Publish: true, FinalHash: finalHash, ContextCompaction: &ContextCompaction{}})
	require.NoError(t, err)
	head, err := rt.SessionStore.(session.ContinuationStore).LoadContinuationHead(t.Context(), "session")
	require.NoError(t, err)
	require.NotNil(t, head)
	assert.Equal(t, "r1", head.ID)
	seed2, err := rt.RecordContentActivity(t.Context(), &api.RecordContentInput{RootRunID: "r2", Items: items, SeedMessages: true, Continue: &api.ContinuationStartInput{SessionID: "session", RunID: "r2", AgentID: "a", Timestamp: time.Unix(3, 0)}})
	require.NoError(t, err)
	assert.Equal(t, "continued", seed2.Continuation.Outcome)
	assert.Equal(t, 2, seed2.Continuation.Pos)
	assert.Nil(t, seed2.Continuation.View.Checkpoint)
	loaded, err := rt.loadView(t.Context(), "r2", "r2", index.ViewRef{IndexHead: seed2.IndexHead})
	require.NoError(t, err)
	encoded, err := json.Marshal(loaded.Messages)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), "choose blue after last compaction")
	assert.Equal(t, 1, seed2.Entries[0].Origin.Pos)
}
