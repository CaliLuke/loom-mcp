package inmem_test

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCancelledSessionOperationsLeaveStoredRunUnchanged(t *testing.T) {
	store := inmem.New()
	now := time.Unix(1, 0).UTC()
	_, err := store.CreateSession(t.Context(), "session", now)
	require.NoError(t, err)
	run := session.RunMeta{RunID: "run", AgentID: "agent", SessionID: "session", Status: session.RunStatusPending}
	require.NoError(t, store.ReserveRun(t.Context(), run, "attempt", ""))
	before, err := store.LoadRun(t.Context(), "run")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tc := range []struct {
		name string
		call func(context.Context) error
	}{
		{"create", func(ctx context.Context) error {
			_, err := store.CreateSession(ctx, "other", now)
			return err
		}},
		{"load session", func(ctx context.Context) error {
			_, err := store.LoadSession(ctx, "session")
			return err
		}},
		{"end", func(ctx context.Context) error {
			_, err := store.EndSession(ctx, "session", now)
			return err
		}},
		{"upsert", func(ctx context.Context) error {
			return store.UpsertRun(ctx, session.RunMeta{RunID: "run", AgentID: "agent", SessionID: "session", Status: session.RunStatusFailed})
		}},
		{"reserve", func(ctx context.Context) error {
			return store.ReserveRun(ctx, run, "attempt", "")
		}},
		{"reject", func(ctx context.Context) error {
			return store.RejectRun(ctx, "run", "attempt")
		}},
		{"link", func(ctx context.Context) error {
			return store.LinkChildRun(ctx, "run", session.RunMeta{RunID: "child", AgentID: "agent", SessionID: "session"})
		}},
		{"load run", func(ctx context.Context) error {
			_, err := store.LoadRun(ctx, "run")
			return err
		}},
		{"list", func(ctx context.Context) error {
			_, err := store.ListRunsBySession(ctx, "session", nil)
			return err
		}},
		{"advance", func(ctx context.Context) error {
			_, err := store.AdvanceContinuation(ctx, "session", nil, session.Continuation{})
			return err
		}},
		{"head", func(ctx context.Context) error {
			_, err := store.LoadContinuationHead(ctx, "session")
			return err
		}},
		{"continuation", func(ctx context.Context) error {
			_, err := store.LoadContinuation(ctx, "session", "id")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorIs(t, tc.call(ctx), context.Canceled)
			after, err := store.LoadRun(t.Context(), "run")
			require.NoError(t, err)
			assert.Equal(t, before, after)
			owner, err := store.LoadSession(t.Context(), "session")
			require.NoError(t, err)
			assert.Equal(t, session.StatusActive, owner.Status)
		})
	}
}

func TestSessionStoreRejectsMissingIdentityAndTime(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*inmem.Store) error
	}{
		{"create identity", func(s *inmem.Store) error {
			_, err := s.CreateSession(t.Context(), "", time.Unix(1, 0))
			return err
		}},
		{"create timestamp", func(s *inmem.Store) error {
			_, err := s.CreateSession(t.Context(), "s", time.Time{})
			return err
		}},
		{"load identity", func(s *inmem.Store) error {
			_, err := s.LoadSession(t.Context(), "")
			return err
		}},
		{"end identity", func(s *inmem.Store) error {
			_, err := s.EndSession(t.Context(), "", time.Unix(1, 0))
			return err
		}},
		{"end timestamp", func(s *inmem.Store) error {
			_, err := s.EndSession(t.Context(), "s", time.Time{})
			return err
		}},
		{"run identity", func(s *inmem.Store) error {
			return s.UpsertRun(t.Context(), session.RunMeta{AgentID: "a", SessionID: "s"})
		}},
		{"agent identity", func(s *inmem.Store) error {
			return s.UpsertRun(t.Context(), session.RunMeta{RunID: "r", SessionID: "s"})
		}},
		{"session identity", func(s *inmem.Store) error {
			return s.UpsertRun(t.Context(), session.RunMeta{RunID: "r", AgentID: "a"})
		}},
		{"load run identity", func(s *inmem.Store) error {
			_, err := s.LoadRun(t.Context(), "")
			return err
		}},
		{"list identity", func(s *inmem.Store) error {
			_, err := s.ListRunsBySession(t.Context(), "", nil)
			return err
		}},
		{"reject identity", func(s *inmem.Store) error {
			return s.RejectRun(t.Context(), "", "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := inmem.New()
			require.Error(t, tc.call(store))
			_, err := store.LoadSession(t.Context(), "s")
			assert.ErrorIs(t, err, session.ErrSessionNotFound)
		})
	}
}
