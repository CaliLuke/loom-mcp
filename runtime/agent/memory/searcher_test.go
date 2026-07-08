package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSearcherQueryContractFiltersAndOrders(t *testing.T) {
	events := []Event{
		NewEvent(time.Unix(30, 0), PlannerNoteData{Note: "late"}, map[string]string{"tenant": "acme"}),
		NewEvent(time.Unix(10, 0), UserMessageData{Message: "hello"}, map[string]string{"tenant": "acme", "session_id": "sess-1"}),
		NewEvent(time.Unix(20, 0), ToolResultData{ToolCallID: "call-1", ToolName: "svc.search"}, map[string]string{"tenant": "other", "session_id": "sess-1"}),
	}
	searcher := SearcherFunc(func(_ context.Context, query Query) (QueryResult, error) {
		require.Equal(t, "svc.agent", query.AgentID)
		require.Equal(t, "run-1", query.RunID)
		require.Equal(t, "sess-1", query.SessionID)
		require.Equal(t, map[string]string{"tenant": "acme"}, query.Labels)
		require.Equal(t, []EventType{EventUserMessage}, query.Types)
		require.Equal(t, 5, query.Limit)
		return QueryEvents(events, query), nil
	})

	result, err := searcher.Query(context.Background(), Query{
		AgentID:   "svc.agent",
		RunID:     "run-1",
		SessionID: "sess-1",
		Labels:    map[string]string{"tenant": "acme"},
		Types:     []EventType{EventUserMessage},
		Limit:     5,
	})
	require.NoError(t, err)
	require.False(t, result.Truncated)
	require.Len(t, result.Events, 1)
	require.Equal(t, EventUserMessage, result.Events[0].Type)
	require.Equal(t, time.Unix(10, 0), result.Events[0].Timestamp)
}

func TestQueryEventsAppliesLimitAndChronologicalOrdering(t *testing.T) {
	events := []Event{
		NewEvent(time.Unix(30, 0), PlannerNoteData{Note: "third"}, map[string]string{"tenant": "acme"}),
		NewEvent(time.Unix(10, 0), PlannerNoteData{Note: "first"}, map[string]string{"tenant": "acme"}),
		NewEvent(time.Unix(20, 0), PlannerNoteData{Note: "second"}, map[string]string{"tenant": "acme"}),
	}

	result := QueryEvents(events, Query{
		Labels: map[string]string{"tenant": "acme"},
		Types:  []EventType{EventPlannerNote},
		Limit:  2,
	})

	require.True(t, result.Truncated)
	require.Len(t, result.Events, 2)
	require.Equal(t, time.Unix(10, 0), result.Events[0].Timestamp)
	require.Equal(t, time.Unix(20, 0), result.Events[1].Timestamp)
}

func TestQueryEventsPreservesInputOrderForEqualTimestamps(t *testing.T) {
	timestamp := time.Unix(10, 0)
	events := []Event{
		NewEvent(timestamp, PlannerNoteData{Note: "first"}, map[string]string{"tenant": "acme"}),
		NewEvent(timestamp, PlannerNoteData{Note: "second"}, map[string]string{"tenant": "acme"}),
		NewEvent(timestamp, PlannerNoteData{Note: "third"}, map[string]string{"tenant": "acme"}),
	}

	result := QueryEvents(events, Query{
		Labels: map[string]string{"tenant": "acme"},
		Types:  []EventType{EventPlannerNote},
	})

	require.Len(t, result.Events, 3)
	require.Equal(t, []string{"first", "second", "third"}, plannerNotes(t, result.Events))
}

func plannerNotes(t *testing.T, events []Event) []string {
	t.Helper()
	notes := make([]string, 0, len(events))
	for _, event := range events {
		data, err := DecodePlannerNoteData(event)
		require.NoError(t, err)
		notes = append(notes, data.Note)
	}
	return notes
}
