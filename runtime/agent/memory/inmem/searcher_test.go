package inmem

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/memory"
	"github.com/stretchr/testify/require"
)

func TestStoreQueryFiltersAppendedEvents(t *testing.T) {
	store := New()
	ctx := context.Background()

	require.NoError(t, store.AppendEvents(ctx, "svc.agent", "run-1",
		memory.NewEvent(time.Unix(30, 0), memory.PlannerNoteData{Note: "late"}, map[string]string{"tenant": "acme", "session_id": "sess-1"}),
		memory.NewEvent(time.Unix(10, 0), memory.UserMessageData{Message: "hello"}, map[string]string{"tenant": "acme", "session_id": "sess-1"}),
		memory.NewEvent(time.Unix(20, 0), memory.ToolResultData{ToolCallID: "call-1", ToolName: "svc.search"}, map[string]string{"tenant": "other", "session_id": "sess-1"}),
	))
	require.NoError(t, store.AppendEvents(ctx, "svc.agent", "run-2",
		memory.NewEvent(time.Unix(5, 0), memory.UserMessageData{Message: "other run"}, map[string]string{"tenant": "acme", "session_id": "sess-1"}),
	))

	result, err := store.Query(ctx, memory.Query{
		AgentID:   "svc.agent",
		RunID:     "run-1",
		SessionID: "sess-1",
		Labels:    map[string]string{"tenant": "acme"},
		Types:     []memory.EventType{memory.EventUserMessage, memory.EventPlannerNote},
		Limit:     10,
	})
	require.NoError(t, err)
	require.False(t, result.Truncated)
	require.Len(t, result.Events, 2)
	require.Equal(t, memory.EventUserMessage, result.Events[0].Type)
	require.Equal(t, memory.EventPlannerNote, result.Events[1].Type)
}

func TestStoreQueryOrdersEqualTimestampRunsDeterministically(t *testing.T) {
	store := New()
	ctx := context.Background()
	timestamp := time.Unix(10, 0)

	require.NoError(t, store.AppendEvents(ctx, "svc.agent", "run-b",
		memory.NewEvent(timestamp, memory.PlannerNoteData{Note: "run-b"}, map[string]string{"tenant": "acme"}),
	))
	require.NoError(t, store.AppendEvents(ctx, "svc.agent", "run-a",
		memory.NewEvent(timestamp, memory.PlannerNoteData{Note: "run-a"}, map[string]string{"tenant": "acme"}),
	))

	result, err := store.Query(ctx, memory.Query{
		AgentID: "svc.agent",
		Labels:  map[string]string{"tenant": "acme"},
		Types:   []memory.EventType{memory.EventPlannerNote},
	})
	require.NoError(t, err)
	require.Len(t, result.Events, 2)
	require.Equal(t, []string{"run-a", "run-b"}, plannerNotes(t, result.Events))
}

func plannerNotes(t *testing.T, events []memory.Event) []string {
	t.Helper()
	notes := make([]string, 0, len(events))
	for _, event := range events {
		data, err := memory.DecodePlannerNoteData(event)
		require.NoError(t, err)
		notes = append(notes, data.Note)
	}
	return notes
}
