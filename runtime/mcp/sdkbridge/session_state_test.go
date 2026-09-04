package sdkbridge

import (
	"context"
	"testing"
	"time"

	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionStateLifecycle(t *testing.T) {
	principal := "alice"
	state := NewSessionState(func(context.Context) string { return principal })
	ctx := mcpruntime.WithSessionID(context.Background(), "session-1")

	assert.False(t, state.IsInitialized(ctx))
	require.ErrorIs(t, state.AssertPrincipal(ctx, "session-1"), errInvalidSessionID)

	state.MarkInitialized("session-1")
	state.CapturePrincipal(ctx, "session-1")
	assert.True(t, state.IsInitialized(ctx))
	require.NoError(t, state.AssertPrincipal(ctx, "session-1"))

	principal = "bob"
	require.ErrorIs(t, state.AssertPrincipal(ctx, "session-1"), errSessionPrincipalMismatch)

	state.Clear("session-1")
	assert.False(t, state.IsInitialized(ctx))
	require.ErrorIs(t, state.AssertPrincipal(ctx, "session-1"), errInvalidSessionID)
}

func TestSessionStateRequiresConfiguredPrincipalBinding(t *testing.T) {
	state := NewSessionState(func(context.Context) string { return "" })
	state.MarkInitialized("session-1")

	require.ErrorIs(t, state.AssertPrincipal(context.Background(), "session-1"), errSessionPrincipalBindingMissing)
}

func TestSessionStatePrunesExpiredSessions(t *testing.T) {
	now := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	state := NewSessionState(nil)
	state.now = func() time.Time { return now }
	state.MarkInitialized("expired")

	now = now.Add(sessionTTL)
	require.ErrorIs(t, state.AssertPrincipal(context.Background(), "expired"), errInvalidSessionID)
}
