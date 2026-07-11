package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestCancellationRegistryScopesRequestsBySessionAndID(t *testing.T) {
	t.Parallel()

	registry := NewRequestCancellationRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	cleanup := registry.Register("session-one", `"request-one"`, cancel)
	defer cleanup()

	assert.False(t, registry.Cancel("session-two", `"request-one"`))
	assert.False(t, registry.Cancel("session-one", `"request-two"`))
	require.NoError(t, ctx.Err())

	require.True(t, registry.Cancel("session-one", `"request-one"`))
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	assert.False(t, registry.Cancel("session-one", `"request-one"`))
}

func TestRequestCancellationRegistryCleanupDoesNotRemoveReplacement(t *testing.T) {
	t.Parallel()

	registry := NewRequestCancellationRegistry()
	firstCtx, firstCancel := context.WithCancel(context.Background())
	firstCleanup := registry.Register("session", "7", firstCancel)

	secondCtx, secondCancel := context.WithCancel(context.Background())
	secondCleanup := registry.Register("session", "7", secondCancel)
	defer secondCleanup()

	require.ErrorIs(t, firstCtx.Err(), context.Canceled)
	firstCleanup()
	require.NoError(t, secondCtx.Err())

	require.True(t, registry.Cancel("session", "7"))
	assert.ErrorIs(t, secondCtx.Err(), context.Canceled)
}

func TestRequestCancellationRegistryCleanupRemovesCompletedRequest(t *testing.T) {
	t.Parallel()

	registry := NewRequestCancellationRegistry()
	_, cancel := context.WithCancel(context.Background())
	cleanup := registry.Register("session", "7", cancel)
	cleanup()

	assert.False(t, registry.Cancel("session", "7"))
}
