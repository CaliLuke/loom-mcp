package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failedSessionListing struct{ session.Store }

func TestSessionLifecycleRejectsMissingIdentityAndKeepsDeletionMonotonic(t *testing.T) {
	rt := New()
	for _, id := range []string{"", " \t"} {
		_, err := rt.CreateSession(t.Context(), id)
		require.ErrorIs(t, err, ErrMissingSessionID)
		_, err = rt.DeleteSession(t.Context(), id)
		require.ErrorIs(t, err, ErrMissingSessionID)
	}
	_, err := rt.DeleteSession(t.Context(), "missing")
	require.ErrorIs(t, err, session.ErrSessionNotFound)
	created, err := rt.CreateSession(t.Context(), "  active  ")
	require.NoError(t, err)
	assert.Equal(t, "active", created.ID)
	ended, err := rt.DeleteSession(t.Context(), " active ")
	require.NoError(t, err)
	assert.Equal(t, session.StatusEnded, ended.Status)
	_, err = rt.CreateSession(t.Context(), "active")
	assert.ErrorIs(t, err, session.ErrSessionEnded)
}

func TestSessionDeletionSurvivesCancellationBackendFailures(t *testing.T) {
	for _, mode := range []string{"listing fails", "cancel by id fails", "cancel unsupported"} {
		t.Run(mode, func(t *testing.T) {
			var eng engine.Engine = &stubEngine{}
			canceler := &recordingCancelByIDEngine{Engine: eng, err: assert.AnError}
			if mode == "cancel by id fails" {
				eng = canceler
			}
			rt := New(WithEngine(eng))
			_, err := rt.CreateSession(t.Context(), "session")
			require.NoError(t, err)
			for _, id := range []string{"one", "two"} {
				require.NoError(t, rt.SessionStore.UpsertRun(t.Context(), session.RunMeta{RunID: id, SessionID: "session", AgentID: "agent", Status: session.RunStatusRunning}))
			}
			if mode == "listing fails" {
				rt.SessionStore = failedSessionListing{rt.SessionStore}
			}
			ended, err := rt.DeleteSession(t.Context(), "session")
			require.NoError(t, err)
			assert.Equal(t, session.StatusEnded, ended.Status)
			persisted, err := rt.SessionStore.LoadSession(t.Context(), "session")
			require.NoError(t, err)
			assert.Equal(t, session.StatusEnded, persisted.Status)
			if mode == "cancel by id fails" {
				assert.ElementsMatch(t, []string{"one", "two"}, canceler.snapshot())
			}
		})
	}
}

func (s failedSessionListing) ListRunsBySession(context.Context, string, []session.RunStatus) ([]session.RunMeta, error) {
	return nil, assert.AnError
}
