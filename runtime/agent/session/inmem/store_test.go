package inmem

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent/session"
)

func TestLinkChildRunValidationErrors(t *testing.T) {
	t.Parallel()

	store := New()
	err := store.LinkChildRun(context.Background(), "", session.RunMeta{
		RunID:     "run-child",
		AgentID:   "agent.child",
		SessionID: "sess-1",
		Status:    session.RunStatusPending,
	})
	require.ErrorIs(t, err, session.ErrParentRunIDRequired)
}

func TestLinkChildRunReturnsSessionMismatchError(t *testing.T) {
	t.Parallel()

	store := New()
	now := time.Now().UTC()
	sess1, err := store.CreateSession(context.Background(), "sess-1", now)
	require.NoError(t, err)
	require.Equal(t, "sess-1", sess1.ID)
	_, err = store.CreateSession(context.Background(), "sess-2", now)
	require.NoError(t, err)
	require.NoError(t, store.UpsertRun(context.Background(), session.RunMeta{
		RunID:     "run-parent",
		AgentID:   "agent.parent",
		SessionID: "sess-1",
		Status:    session.RunStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}))

	err = store.LinkChildRun(context.Background(), "run-parent", session.RunMeta{
		RunID:     "run-child",
		AgentID:   "agent.child",
		SessionID: "sess-2",
		Status:    session.RunStatusPending,
	})
	require.ErrorIs(t, err, session.ErrRunSessionMismatch)
}

func TestUpsertRunDoesNotClobberLinkedChildren(t *testing.T) {
	t.Parallel()

	store := New()
	now := time.Now().UTC()
	_, err := store.CreateSession(context.Background(), "sess-1", now)
	require.NoError(t, err)
	require.NoError(t, store.UpsertRun(context.Background(), session.RunMeta{
		RunID:     "run-parent",
		AgentID:   "agent.parent",
		SessionID: "sess-1",
		Status:    session.RunStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}))

	// Simulate a hook handler's load-modify-write: it loads the parent before
	// any child is linked...
	stale, err := store.LoadRun(context.Background(), "run-parent")
	require.NoError(t, err)
	require.Empty(t, stale.ChildRunIDs)

	// ...then LinkChildRun commits a child concurrently...
	require.NoError(t, store.LinkChildRun(context.Background(), "run-parent", session.RunMeta{
		RunID:     "run-child",
		AgentID:   "agent.child",
		SessionID: "sess-1",
		Status:    session.RunStatusPending,
	}))

	// ...and the hook writes back its stale snapshot. The link must survive.
	stale.Status = session.RunStatusCompleted
	require.NoError(t, store.UpsertRun(context.Background(), stale))

	parent, err := store.LoadRun(context.Background(), "run-parent")
	require.NoError(t, err)
	require.Equal(t, session.RunStatusCompleted, parent.Status)
	require.Equal(t, []string{"run-child"}, parent.ChildRunIDs)
}

func TestLinkChildRunIsIdempotent(t *testing.T) {
	t.Parallel()

	store := New()
	now := time.Now().UTC()
	_, err := store.CreateSession(context.Background(), "sess-1", now)
	require.NoError(t, err)
	require.NoError(t, store.UpsertRun(context.Background(), session.RunMeta{
		RunID:     "run-parent",
		AgentID:   "agent.parent",
		SessionID: "sess-1",
		Status:    session.RunStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}))

	child := session.RunMeta{
		RunID:     "run-child",
		AgentID:   "agent.child",
		SessionID: "sess-1",
		Status:    session.RunStatusPending,
	}
	for range 3 {
		require.NoError(t, store.LinkChildRun(context.Background(), "run-parent", child))
	}

	parent, err := store.LoadRun(context.Background(), "run-parent")
	require.NoError(t, err)
	require.Equal(t, []string{"run-child"}, parent.ChildRunIDs)
}
