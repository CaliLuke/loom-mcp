package inmem

import (
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReserveRunIsCreateOnlyAndReplayIdempotent(t *testing.T) {
	store := New()
	ctx := t.Context()
	_, err := store.CreateSession(ctx, "session", time.Unix(1, 0))
	require.NoError(t, err)

	run := session.RunMeta{
		RunID:     "run",
		AgentID:   "agent",
		SessionID: "session",
		Status:    session.RunStatusPending,
		Metadata:  map[string]any{"source": "first"},
	}
	require.NoError(t, store.ReserveRun(ctx, run, "attempt-a", ""))

	replay := run
	replay.Metadata = map[string]any{"source": "replay"}
	require.NoError(t, store.ReserveRun(ctx, replay, "attempt-a", ""))
	stored, err := store.LoadRun(ctx, "run")
	require.NoError(t, err)
	assert.Equal(t, session.RunStatusPending, stored.Status)
	assert.Equal(t, "first", stored.Metadata["source"])

	require.ErrorIs(t, store.ReserveRun(ctx, replay, "attempt-b", ""), session.ErrRunAlreadyExists)
	require.ErrorIs(t, store.ReserveRun(ctx, session.RunMeta{
		RunID: "run", AgentID: "other-agent", SessionID: "session", Status: session.RunStatusPending,
	}, "attempt-a", ""), session.ErrRunAlreadyExists)
	require.ErrorIs(t, store.ReserveRun(ctx, session.RunMeta{
		RunID: "run", AgentID: "agent", SessionID: "other-session", Status: session.RunStatusPending,
	}, "attempt-a", ""), session.ErrRunAlreadyExists)
	stored, err = store.LoadRun(ctx, "run")
	require.NoError(t, err)
	assert.Equal(t, session.RunStatusPending, stored.Status)
	assert.Equal(t, "first", stored.Metadata["source"])
}

func TestRejectRunOnlyFailsOwnedPendingReservation(t *testing.T) {
	store := New()
	ctx := t.Context()
	_, err := store.CreateSession(ctx, "session", time.Unix(1, 0))
	require.NoError(t, err)
	input := session.RunMeta{
		RunID:     "run",
		AgentID:   "agent",
		SessionID: "session",
		Status:    session.RunStatusPending,
		Metadata:  map[string]any{"source": "original"},
	}
	require.NoError(t, store.ReserveRun(ctx, input, "attempt-a", ""))

	require.NoError(t, store.RejectRun(ctx, "run", "attempt-b"))
	stored, err := store.LoadRun(ctx, "run")
	require.NoError(t, err)
	assert.Equal(t, session.RunStatusPending, stored.Status)

	require.NoError(t, store.RejectRun(ctx, "run", "attempt-a"))
	stored, err = store.LoadRun(ctx, "run")
	require.NoError(t, err)
	assert.Equal(t, session.RunStatusFailed, stored.Status)
	assert.Equal(t, "original", stored.Metadata["source"])

	updated := stored
	updated.Status = session.RunStatusRunning
	require.NoError(t, store.UpsertRun(ctx, updated))
	require.NoError(t, store.RejectRun(ctx, "run", "attempt-a"))
	stored, err = store.LoadRun(ctx, "run")
	require.NoError(t, err)
	assert.Equal(t, session.RunStatusFailed, stored.Status, "terminal status cannot be reopened")

	running := input
	running.RunID = "running"
	require.NoError(t, store.ReserveRun(ctx, running, "attempt-running", ""))
	running.Status = session.RunStatusRunning
	require.NoError(t, store.UpsertRun(ctx, running))
	require.NoError(t, store.RejectRun(ctx, "running", "attempt-running"))
	stored, err = store.LoadRun(ctx, "running")
	require.NoError(t, err)
	assert.Equal(t, session.RunStatusRunning, stored.Status)
	require.NoError(t, store.RejectRun(ctx, "missing", "attempt-running"))
}

func TestReserveChildRunAtomicallyLinksOnlyActiveSession(t *testing.T) {
	store := New()
	ctx := t.Context()
	_, err := store.CreateSession(ctx, "session", time.Unix(1, 0))
	require.NoError(t, err)
	parent := session.RunMeta{RunID: "parent", AgentID: "agent", SessionID: "session", Status: session.RunStatusRunning}
	require.NoError(t, store.UpsertRun(ctx, parent))
	child := session.RunMeta{RunID: "child", AgentID: "child-agent", SessionID: "session", Status: session.RunStatusPending}

	require.NoError(t, store.ReserveRun(ctx, child, "child-attempt", "parent"))
	require.NoError(t, store.ReserveRun(ctx, child, "child-attempt", "parent"))
	storedParent, err := store.LoadRun(ctx, "parent")
	require.NoError(t, err)
	assert.Equal(t, []string{"child"}, storedParent.ChildRunIDs)

	require.NoError(t, store.RejectRun(ctx, "child", "child-attempt"))
	storedChild, err := store.LoadRun(ctx, "child")
	require.NoError(t, err)
	assert.Equal(t, session.RunStatusFailed, storedChild.Status)
	storedParent, err = store.LoadRun(ctx, "parent")
	require.NoError(t, err)
	assert.Equal(t, []string{"child"}, storedParent.ChildRunIDs)

	_, err = store.EndSession(ctx, "session", time.Unix(2, 0))
	require.NoError(t, err)
	// The runtime may replay the already committed link event after session end.
	// That replay is a no-op; it must not be rejected as a new admission.
	require.NoError(t, store.LinkChildRun(ctx, "parent", session.RunMeta{
		RunID: "child", AgentID: "child-agent", SessionID: "session", Status: session.RunStatusPending,
	}))
	require.ErrorIs(t, store.ReserveRun(ctx, session.RunMeta{
		RunID: "new-child", AgentID: "child-agent", SessionID: "session", Status: session.RunStatusPending,
	}, "new-attempt", "parent"), session.ErrSessionEnded)
	require.ErrorIs(t, store.LinkChildRun(ctx, "parent", session.RunMeta{
		RunID: "new-child", AgentID: "child-agent", SessionID: "session", Status: session.RunStatusPending,
	}), session.ErrSessionEnded)
	storedParent, err = store.LoadRun(ctx, "parent")
	require.NoError(t, err)
	assert.Equal(t, []string{"child"}, storedParent.ChildRunIDs)
}
