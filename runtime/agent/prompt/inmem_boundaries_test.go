package prompt

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromptStoreListIsOrderedAndOwnsReturnedData(t *testing.T) {
	store := NewInMemoryStore()
	labels := map[string]string{"tenant": "one"}
	metadata := map[string]string{"owner": "original"}
	require.NoError(t, store.Set(t.Context(), "first", Scope{Labels: labels}, "old", metadata))
	require.NoError(t, store.Set(t.Context(), "second", Scope{}, "new", nil))
	labels["tenant"] = "changed"
	metadata["owner"] = "changed"
	listed, err := store.List(t.Context())
	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, Ident("second"), listed[0].PromptID)
	assert.Equal(t, Ident("first"), listed[1].PromptID)
	assert.Equal(t, "one", listed[1].Scope.Labels["tenant"])
	assert.Equal(t, "original", listed[1].Metadata["owner"])
	listed[1].Scope.Labels["tenant"] = "reader mutation"
	listed[1].Metadata["owner"] = "reader mutation"
	resolved, err := store.Resolve(t.Context(), "first", Scope{Labels: map[string]string{"tenant": "one"}})
	require.NoError(t, err)
	require.NotNil(t, resolved)
	assert.Equal(t, "original", resolved.Metadata["owner"])
	history, err := store.History(t.Context(), "first")
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, "old", history[0].Template)
}

func TestPromptStoreRejectsMissingIdentityAndCanceledCalls(t *testing.T) {
	store := NewInMemoryStore()
	require.Error(t, store.Set(t.Context(), "", Scope{}, "template", nil))
	require.Error(t, store.Set(t.Context(), "id", Scope{}, "", nil))
	_, err := store.Resolve(t.Context(), "", Scope{})
	require.Error(t, err)
	_, err = store.History(t.Context(), "")
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, store.Set(ctx, "id", Scope{}, "template", nil), context.Canceled)
	_, err = store.Resolve(ctx, "id", Scope{})
	require.ErrorIs(t, err, context.Canceled)
	_, err = store.History(ctx, "id")
	require.ErrorIs(t, err, context.Canceled)
	_, err = store.List(ctx)
	require.ErrorIs(t, err, context.Canceled)
	entries, err := store.List(t.Context())
	require.NoError(t, err)
	assert.Empty(t, entries)
}
