package inmem

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
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

func TestUpsertRunDoesNotReopenTerminalRun(t *testing.T) {
	store := New()
	ctx := context.Background()
	_, err := store.CreateSession(ctx, "sess-terminal", time.Unix(1, 0).UTC())
	require.NoError(t, err)
	run := session.RunMeta{
		RunID:     "run-terminal",
		AgentID:   "agent.chat",
		SessionID: "sess-terminal",
		Status:    session.RunStatusCompleted,
	}
	require.NoError(t, store.UpsertRun(ctx, run))

	run.Status = session.RunStatusRunning
	run.Metadata = map[string]any{"late": true}
	require.NoError(t, store.UpsertRun(ctx, run))

	stored, err := store.LoadRun(ctx, run.RunID)
	require.NoError(t, err)
	require.Equal(t, session.RunStatusCompleted, stored.Status)
	require.Equal(t, true, stored.Metadata["late"])
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
