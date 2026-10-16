package inmem_test

import (
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory/inmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryAcrossAgentsIsOrderedIsolatedAndResettable(t *testing.T) {
	store := inmem.New()
	for _, entry := range []struct{ agent, run, text string }{
		{"z", "b", "last"}, {"a", "b", "second"}, {"a", "a", "first"},
	} {
		require.NoError(t, store.AppendEvents(t.Context(), entry.agent, entry.run, memory.Event{
			Type: memory.EventUserMessage, Timestamp: time.Unix(1, 0), Labels: map[string]string{"session_id": "shared"}, Data: map[string]any{"text": entry.text},
		}))
	}
	result, err := store.Query(t.Context(), memory.Query{SessionID: "shared", Limit: 2})
	require.NoError(t, err)
	require.Len(t, result.Events, 2)
	assert.True(t, result.Truncated)
	assert.Equal(t, "first", result.Events[0].Data.(map[string]any)["text"])
	assert.Equal(t, "second", result.Events[1].Data.(map[string]any)["text"])
	result.Events[0].Data.(map[string]any)["text"] = "changed"
	result.Events[0].Labels["session_id"] = "changed"
	snapshot, err := store.LoadRun(t.Context(), "a", "a")
	require.NoError(t, err)
	assert.Equal(t, "first", snapshot.Events[0].Data.(map[string]any)["text"])
	assert.Equal(t, "shared", snapshot.Events[0].Labels["session_id"])
	store.Reset()
	empty, err := store.Query(t.Context(), memory.Query{})
	require.NoError(t, err)
	assert.Empty(t, empty.Events)
	assert.False(t, empty.Truncated)
	require.NoError(t, store.AppendEvents(t.Context(), "a", "a", memory.Event{Type: memory.EventToolCall}))
	reloaded, err := store.LoadRun(t.Context(), "a", "a")
	require.NoError(t, err)
	require.Len(t, reloaded.Events, 1)
	assert.Equal(t, memory.EventToolCall, reloaded.Events[0].Type)
}
