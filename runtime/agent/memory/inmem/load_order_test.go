package inmem

import (
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadRunOrdersTimestampsWithStableTies(t *testing.T) {
	store := New()
	require.NoError(t, store.AppendEvents(t.Context(), "a", "r",
		memory.Event{Timestamp: time.Unix(3, 0), Labels: map[string]string{"id": "last"}},
		memory.Event{Timestamp: time.Unix(2, 0), Labels: map[string]string{"id": "tie-first"}},
	))
	require.NoError(t, store.AppendEvents(t.Context(), "a", "r",
		memory.Event{Timestamp: time.Unix(1, 0), Labels: map[string]string{"id": "first"}},
		memory.Event{Timestamp: time.Unix(2, 0), Labels: map[string]string{"id": "tie-second"}},
	))
	snapshot, err := store.LoadRun(t.Context(), "a", "r")
	require.NoError(t, err)
	require.Len(t, snapshot.Events, 4)
	assert.Equal(t, []string{"first", "tie-first", "tie-second", "last"}, eventIDs(snapshot.Events))
	snapshot.Events[0].Labels["id"] = "mutated"
	snapshot.Events[0], snapshot.Events[3] = snapshot.Events[3], snapshot.Events[0]
	again, err := store.LoadRun(t.Context(), "a", "r")
	require.NoError(t, err)
	assert.Equal(t, []string{"first", "tie-first", "tie-second", "last"}, eventIDs(again.Events))
}

func eventIDs(events []memory.Event) []string {
	ids := make([]string, len(events))
	for i, event := range events {
		ids[i] = event.Labels["id"]
	}
	return ids
}
