package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStreamableHTTPSessionsLifecycle(t *testing.T) {
	t.Parallel()

	store := NewStreamableHTTPSessions()
	require.False(t, store.HasIssued())
	require.ErrorIs(t, store.Validate(""), ErrInvalidSessionID)
	require.ErrorIs(t, store.Validate("missing"), ErrInvalidSessionID)

	require.NoError(t, store.Issue("sess-1"))
	require.True(t, store.HasIssued())
	require.NoError(t, store.Validate("sess-1"))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(done)
	}()
	unregister, err := store.RegisterListener("sess-1", cancel)
	require.NoError(t, err)
	t.Cleanup(unregister)

	require.NoError(t, store.Terminate("sess-1"))
	<-done
	require.False(t, store.HasIssued())
	require.ErrorIs(t, store.Validate("sess-1"), ErrSessionTerminated)
	require.ErrorIs(t, store.Terminate("sess-1"), ErrSessionTerminated)
}

func TestStreamableHTTPSessionsUnregisterListener(t *testing.T) {
	t.Parallel()

	store := NewStreamableHTTPSessions()
	require.NoError(t, store.Issue("sess-1"))
	ctx, cancel := context.WithCancel(context.Background())
	unregister, err := store.RegisterListener("sess-1", cancel)
	require.NoError(t, err)
	unregister()

	require.NoError(t, store.Terminate("sess-1"))
	require.NoError(t, ctx.Err())
}

func TestStreamableHTTPSessionsRejectsLateRegistration(t *testing.T) {
	t.Parallel()

	store := NewStreamableHTTPSessions()
	require.NoError(t, store.Issue("sess-1"))
	require.NoError(t, store.Terminate("sess-1"))

	_, err := store.RegisterListener("sess-1", func() {})
	require.ErrorIs(t, err, ErrSessionTerminated)
}

func TestStreamableHTTPSessionsExpiresIssuedSessions(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	store := newStreamableHTTPSessions(streamableHTTPSessionConfig{
		issuedTTL: time.Minute,
		now: func() time.Time {
			return now
		},
	})

	require.NoError(t, store.Issue("sess-1"))
	require.NoError(t, store.Validate("sess-1"))

	now = now.Add(time.Minute)
	require.ErrorIs(t, store.Validate("sess-1"), ErrInvalidSessionID)
	require.False(t, store.HasIssued())
}

func TestStreamableHTTPSessionsEvictsOldestIssuedSessionWhenBounded(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	store := newStreamableHTTPSessions(streamableHTTPSessionConfig{
		maxIssued: 2,
		now: func() time.Time {
			return now
		},
	})

	require.NoError(t, store.Issue("sess-1"))
	now = now.Add(time.Second)
	require.NoError(t, store.Issue("sess-2"))
	now = now.Add(time.Second)
	require.NoError(t, store.Issue("sess-3"))

	require.ErrorIs(t, store.Validate("sess-1"), ErrInvalidSessionID)
	require.NoError(t, store.Validate("sess-2"))
	require.NoError(t, store.Validate("sess-3"))
}

func TestStreamableHTTPSessionsIssuePreservesNewSessionWhenOlderSessionsHaveListeners(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	store := newStreamableHTTPSessions(streamableHTTPSessionConfig{
		maxIssued: 2,
		now: func() time.Time {
			return now
		},
	})

	require.NoError(t, store.Issue("sess-1"))
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	_, err := store.RegisterListener("sess-1", cancel1)
	require.NoError(t, err)

	now = now.Add(time.Second)
	require.NoError(t, store.Issue("sess-2"))
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	_, err = store.RegisterListener("sess-2", cancel2)
	require.NoError(t, err)

	now = now.Add(time.Second)
	require.NoError(t, store.Issue("sess-3"))

	require.ErrorIs(t, store.Validate("sess-1"), ErrInvalidSessionID)
	require.NoError(t, store.Validate("sess-2"))
	require.NoError(t, store.Validate("sess-3"))
	require.ErrorIs(t, ctx1.Err(), context.Canceled)
	require.NoError(t, ctx2.Err())
}

func TestStreamableHTTPSessionsIssueRejectsInvalidReceiverAndID(t *testing.T) {
	t.Parallel()

	var nilStore *StreamableHTTPSessions
	require.ErrorIs(t, nilStore.Issue("sess-1"), ErrInvalidSessionID)
	require.ErrorIs(t, NewStreamableHTTPSessions().Issue(""), ErrInvalidSessionID)
}

func TestStreamableHTTPSessionsPrunesTerminatedTombstones(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	store := newStreamableHTTPSessions(streamableHTTPSessionConfig{
		terminatedTTL: time.Minute,
		now: func() time.Time {
			return now
		},
	})

	require.NoError(t, store.Issue("sess-1"))
	require.NoError(t, store.Terminate("sess-1"))
	require.ErrorIs(t, store.Validate("sess-1"), ErrSessionTerminated)

	now = now.Add(time.Minute)
	require.ErrorIs(t, store.Validate("sess-1"), ErrInvalidSessionID)
	require.ErrorIs(t, store.Terminate("sess-1"), ErrInvalidSessionID)
	require.Empty(t, store.terminated)
}

func TestStreamableHTTPSessionsPrincipalLifecycle(t *testing.T) {
	t.Parallel()

	store := NewStreamableHTTPSessions()
	require.NoError(t, store.IssueForPrincipal("sess-1", "user-1"))
	require.NoError(t, store.ValidateForPrincipal("sess-1", "user-1"))
	require.ErrorIs(t, store.ValidateForPrincipal("sess-1", ""), ErrSessionPrincipalMismatch)
	require.ErrorIs(t, store.ValidateForPrincipal("sess-1", "user-2"), ErrSessionPrincipalMismatch)
	require.ErrorIs(t, store.TerminateForPrincipal("sess-1", "user-2"), ErrSessionPrincipalMismatch)
	require.NoError(t, store.ValidateForPrincipal("sess-1", "user-1"))
	require.NoError(t, store.TerminateForPrincipal("sess-1", "user-1"))
	require.ErrorIs(t, store.ValidateForPrincipal("sess-1", "user-1"), ErrSessionTerminated)
}

func TestStreamableHTTPSessionsRejectsAuthenticatedAdoptionOfAnonymousSession(t *testing.T) {
	t.Parallel()

	store := NewStreamableHTTPSessions()
	require.NoError(t, store.Issue("sess-1"))
	require.NoError(t, store.ValidateForPrincipal("sess-1", ""))
	require.ErrorIs(t, store.ValidateForPrincipal("sess-1", "user-1"), ErrSessionPrincipalBindingMissing)
	_, err := store.RegisterListenerForPrincipal("sess-1", "user-1", func() {})
	require.ErrorIs(t, err, ErrSessionPrincipalBindingMissing)
	require.ErrorIs(t, store.TerminateForPrincipal("sess-1", "user-1"), ErrSessionPrincipalBindingMissing)
	require.NoError(t, store.Validate("sess-1"), "a rejected adoption must not invalidate the session")
}
