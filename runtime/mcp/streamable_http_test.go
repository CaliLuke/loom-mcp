package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamableHTTPSessionsLifecycle(t *testing.T) {
	t.Parallel()

	store := NewStreamableHTTPSessions()
	require.False(t, store.HasIssued())
	require.ErrorIs(t, store.Validate(""), ErrInvalidSessionID)
	require.ErrorIs(t, store.Validate("missing"), ErrInvalidSessionID)

	store.Issue("sess-1")
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
	store.Issue("sess-1")
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
	store.Issue("sess-1")
	require.NoError(t, store.Terminate("sess-1"))

	_, err := store.RegisterListener("sess-1", func() {})
	require.ErrorIs(t, err, ErrSessionTerminated)
}
