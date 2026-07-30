package inmem

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionLifecycleAndRunQueries(t *testing.T) {
	t.Parallel()

	store := New()
	ctx := context.Background()
	createdAt := time.Unix(10, 0).UTC()
	created, err := store.CreateSession(ctx, "sess-1", createdAt)
	require.NoError(t, err)
	assert.Equal(t, session.StatusActive, created.Status)
	assert.Equal(t, createdAt, created.CreatedAt)

	idempotent, err := store.CreateSession(ctx, "sess-1", createdAt.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, created, idempotent)
	loaded, err := store.LoadSession(ctx, "sess-1")
	require.NoError(t, err)
	assert.Equal(t, created, loaded)

	running := session.RunMeta{
		RunID:     "run-1",
		AgentID:   "agent.chat",
		SessionID: "sess-1",
		Status:    session.RunStatusRunning,
		Labels:    map[string]string{"tenant": "acme"},
		PromptRefs: []prompt.PromptRef{
			{ID: "agent.system", Version: "v1"},
		},
		Metadata: map[string]any{"attempt": 1},
	}
	require.NoError(t, store.UpsertRun(ctx, running))
	require.NoError(t, store.UpsertRun(ctx, session.RunMeta{
		RunID:     "run-2",
		AgentID:   "agent.chat",
		SessionID: "sess-1",
		Status:    session.RunStatusCompleted,
	}))
	_, err = store.CreateSession(ctx, "sess-2", createdAt)
	require.NoError(t, err)
	require.NoError(t, store.UpsertRun(ctx, session.RunMeta{
		RunID:     "run-other",
		AgentID:   "agent.chat",
		SessionID: "sess-2",
		Status:    session.RunStatusRunning,
	}))

	runs, err := store.ListRunsBySession(ctx, "sess-1", []session.RunStatus{session.RunStatusRunning})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "run-1", runs[0].RunID)

	// Stored and loaded values must not alias caller-owned containers.
	running.Labels["tenant"] = "mutated"
	running.PromptRefs[0].Version = "mutated"
	running.Metadata["attempt"] = 99
	runs[0].Labels["tenant"] = "loaded-mutation"
	stored, err := store.LoadRun(ctx, "run-1")
	require.NoError(t, err)
	assert.Equal(t, "acme", stored.Labels["tenant"])
	assert.Equal(t, "v1", stored.PromptRefs[0].Version)
	assert.Equal(t, 1, stored.Metadata["attempt"])

	endedAt := createdAt.Add(2 * time.Hour)
	ended, err := store.EndSession(ctx, "sess-1", endedAt)
	require.NoError(t, err)
	assert.Equal(t, session.StatusEnded, ended.Status)
	require.NotNil(t, ended.EndedAt)
	assert.Equal(t, endedAt, *ended.EndedAt)
	idempotentEnd, err := store.EndSession(ctx, "sess-1", endedAt.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, ended, idempotentEnd)
	_, err = store.CreateSession(ctx, "sess-1", createdAt)
	require.ErrorIs(t, err, session.ErrSessionEnded)

	// Terminal sessions reject new runs but still accept terminal updates to
	// runs that were already active when the session ended.
	err = store.UpsertRun(ctx, session.RunMeta{RunID: "run-new", AgentID: "agent.chat", SessionID: "sess-1", Status: session.RunStatusPending})
	require.ErrorIs(t, err, session.ErrSessionEnded)
	stored.Status = session.RunStatusCanceled
	require.NoError(t, store.UpsertRun(ctx, stored))
}

func TestUpsertRunRequiresExistingSessionAndStableAssociation(t *testing.T) {
	t.Parallel()

	store := New()
	ctx := context.Background()
	run := session.RunMeta{RunID: "run-1", AgentID: "agent.chat", SessionID: "missing", Status: session.RunStatusPending}
	require.ErrorIs(t, store.UpsertRun(ctx, run), session.ErrSessionNotFound)

	_, err := store.CreateSession(ctx, "sess-1", time.Now().UTC())
	require.NoError(t, err)
	run.SessionID = "sess-1"
	require.NoError(t, store.UpsertRun(ctx, run))
	run.SessionID = "sess-2"
	require.ErrorIs(t, store.UpsertRun(ctx, run), session.ErrRunSessionImmutable)
}

func TestStoreHonorsCanceledContexts(t *testing.T) {
	t.Parallel()

	store := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := store.CreateSession(ctx, "sess-1", time.Now().UTC())
	require.ErrorIs(t, err, context.Canceled)
	_, err = store.LoadSession(context.Background(), "sess-1")
	require.ErrorIs(t, err, session.ErrSessionNotFound)
}
