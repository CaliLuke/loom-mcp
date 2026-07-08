package memory

import (
	"context"
	"slices"
	"time"
)

type (
	// Searcher provides indexed or cross-run memory lookup without changing the
	// append/load Store contract.
	Searcher interface {
		Query(ctx context.Context, query Query) (QueryResult, error)
	}

	// SearcherFunc adapts a function to Searcher.
	SearcherFunc func(ctx context.Context, query Query) (QueryResult, error)

	// Query filters memory events. AgentID and RunID scope stores that can
	// address those dimensions directly; SessionID is matched from event labels.
	Query struct {
		AgentID   string
		RunID     string
		SessionID string
		Labels    map[string]string
		Types     []EventType
		Limit     int
	}

	// QueryResult contains matching memory events in chronological order.
	QueryResult struct {
		Events    []Event
		Truncated bool
	}
)

const sessionIDLabel = "session_id"

// Query calls f(ctx, query).
func (f SearcherFunc) Query(ctx context.Context, query Query) (QueryResult, error) {
	return f(ctx, query)
}

// QueryEvents filters events in memory using query labels, event types, session,
// and limit. Returned events are chronological and defensively copied.
func QueryEvents(events []Event, query Query) QueryResult {
	if len(events) == 0 {
		return QueryResult{}
	}
	typeSet := eventTypeSet(query.Types)
	matches := make([]Event, 0, len(events))
	for _, event := range events {
		if len(typeSet) > 0 {
			if _, ok := typeSet[event.Type]; !ok {
				continue
			}
		}
		if query.SessionID != "" && event.Labels[sessionIDLabel] != query.SessionID {
			continue
		}
		if !eventLabelsMatch(event.Labels, query.Labels) {
			continue
		}
		matches = append(matches, cloneEvent(event))
	}
	slices.SortStableFunc(matches, func(a, b Event) int {
		return compareEventTime(a.Timestamp, b.Timestamp)
	})
	truncated := false
	if query.Limit > 0 && len(matches) > query.Limit {
		truncated = true
		matches = matches[:query.Limit]
	}
	return QueryResult{Events: matches, Truncated: truncated}
}

func eventTypeSet(types []EventType) map[EventType]struct{} {
	if len(types) == 0 {
		return nil
	}
	out := make(map[EventType]struct{}, len(types))
	for _, typ := range types {
		out[typ] = struct{}{}
	}
	return out
}

func eventLabelsMatch(labels, query map[string]string) bool {
	for key, want := range query {
		if labels[key] != want {
			return false
		}
	}
	return true
}

func compareEventTime(a, b time.Time) int {
	switch {
	case a.Before(b):
		return -1
	case a.After(b):
		return 1
	default:
		return 0
	}
}

func cloneEvent(event Event) Event {
	event.Labels = cloneEventLabels(event.Labels)
	return event
}
