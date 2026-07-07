package inmem

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/memory"
	"github.com/stretchr/testify/require"
)

func TestServicePutEntrySearchesByScopeLabelsAndLimit(t *testing.T) {
	ctx := context.Background()
	service := NewService()
	userScope := memory.Scope{Namespace: "workspace-a", UserID: "user-1", Visibility: memory.VisibilityUser}
	otherScope := memory.Scope{Namespace: "workspace-a", UserID: "user-2", Visibility: memory.VisibilityUser}

	_, err := service.PutEntry(ctx, memory.PutEntryInput{
		Scope:     userScope,
		Content:   "Luca prefers terse implementation plans",
		Author:    "user",
		Timestamp: time.Unix(20, 0),
		Labels:    map[string]string{"project": "loom"},
	})
	require.NoError(t, err)
	_, err = service.PutEntry(ctx, memory.PutEntryInput{
		Scope:     userScope,
		Content:   "Luca uses Postgres for durable product storage",
		Author:    "user",
		Timestamp: time.Unix(30, 0),
		Labels:    map[string]string{"project": "autok"},
	})
	require.NoError(t, err)
	_, err = service.PutEntry(ctx, memory.PutEntryInput{
		Scope:     otherScope,
		Content:   "Other user memory about implementation plans",
		Author:    "user",
		Timestamp: time.Unix(40, 0),
		Labels:    map[string]string{"project": "loom"},
	})
	require.NoError(t, err)

	result, err := service.Search(ctx, memory.SearchQuery{
		Scope:  userScope,
		Query:  "implementation plans",
		Labels: map[string]string{"project": "loom"},
		Limit:  1,
	})
	require.NoError(t, err)
	require.False(t, result.Truncated)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "Luca prefers terse implementation plans", result.Hits[0].Entry.Content)
	require.Equal(t, userScope, result.Hits[0].Entry.Scope)
}

func TestServiceIngestRunAndEventsExtractTextualEntries(t *testing.T) {
	ctx := context.Background()
	service := NewService()
	scope := memory.Scope{Namespace: "workspace-a", UserID: "user-1", Visibility: memory.VisibilityUser}

	runResult, err := service.IngestRun(ctx, memory.IngestRunInput{
		Scope:     scope,
		AgentID:   "svc.agent",
		SessionID: "sess-1",
		RunID:     "run-1",
		Events: []memory.Event{
			memory.NewEvent(time.Unix(10, 0), memory.UserMessageData{Message: "Remember the release checklist"}, nil),
			memory.NewEvent(time.Unix(11, 0), memory.ToolCallData{ToolCallID: "call-1", ToolName: "svc.lookup"}, nil),
			memory.NewEvent(time.Unix(12, 0), memory.AssistantMessageData{Message: "The checklist has lint and tests"}, nil),
		},
		Labels: map[string]string{"release": "v1"},
	})
	require.NoError(t, err)
	require.Len(t, runResult.Entries, 2)
	require.Equal(t, 1, runResult.Skipped)

	deltaResult, err := service.IngestEvents(ctx, memory.IngestEventsInput{
		Scope:        scope,
		AgentID:      "svc.agent",
		SessionID:    "sess-1",
		RunID:        "run-1",
		StartOrdinal: 3,
		Events: []memory.Event{
			memory.NewEvent(time.Unix(13, 0), memory.PlannerNoteData{Note: "Release requires generated fixture verification"}, nil),
		},
	})
	require.NoError(t, err)
	require.Len(t, deltaResult.Entries, 1)

	result, err := service.Search(ctx, memory.SearchQuery{Scope: scope, Query: "fixture verification"})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	require.Equal(t, "planner", result.Hits[0].Entry.Author)
	require.Equal(t, memory.SourceRunEvent, result.Hits[0].Entry.Sources[0].Kind)
	require.Equal(t, 3, result.Hits[0].Entry.Sources[0].EventOrdinal)
}

func TestServiceDefensiveCopiesAndSourceIdempotency(t *testing.T) {
	ctx := context.Background()
	service := NewService()
	scope := memory.Scope{Namespace: "workspace-a", UserID: "user-1", Visibility: memory.VisibilityUser}
	labels := map[string]string{"topic": "memory"}
	metadata := map[string]any{"source": "test"}
	sources := []memory.SourceRef{{Kind: memory.SourceDirect, IdempotencyKey: "memory-1"}}

	first, err := service.PutEntry(ctx, memory.PutEntryInput{
		Scope:    scope,
		Content:  "Long-term memory keeps entries separate from transcript events",
		Author:   "user",
		Sources:  sources,
		Labels:   labels,
		Metadata: metadata,
	})
	require.NoError(t, err)
	labels["topic"] = "mutated"
	metadata["source"] = "mutated"
	sources[0].IdempotencyKey = "mutated"

	second, err := service.PutEntry(ctx, memory.PutEntryInput{
		Scope:   scope,
		Content: "Duplicate should return the first entry",
		Author:  "user",
		Sources: []memory.SourceRef{{Kind: memory.SourceDirect, IdempotencyKey: "memory-1"}},
	})
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, first.Content, second.Content)

	result, err := service.Search(ctx, memory.SearchQuery{Scope: scope, Query: "transcript", Labels: map[string]string{"topic": "memory"}})
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	result.Hits[0].Entry.Labels["topic"] = "mutated"

	again, err := service.Search(ctx, memory.SearchQuery{Scope: scope, Query: "transcript", Labels: map[string]string{"topic": "memory"}})
	require.NoError(t, err)
	require.Len(t, again.Hits, 1)
	require.Equal(t, "memory", again.Hits[0].Entry.Labels["topic"])
	require.Equal(t, "test", again.Hits[0].Entry.Metadata["source"])
}

func TestServiceSearchTruncatesAndSeparatesSharedFromUserMemory(t *testing.T) {
	ctx := context.Background()
	service := NewService()
	userScope := memory.Scope{Namespace: "workspace-a", UserID: "user-1", Visibility: memory.VisibilityUser}
	sharedScope := memory.Scope{Namespace: "workspace-a", Visibility: memory.VisibilityShared}

	_, err := service.PutEntry(ctx, memory.PutEntryInput{Scope: userScope, Content: "memory alpha", Author: "user", Timestamp: time.Unix(10, 0)})
	require.NoError(t, err)
	_, err = service.PutEntry(ctx, memory.PutEntryInput{Scope: userScope, Content: "memory beta", Author: "user", Timestamp: time.Unix(20, 0)})
	require.NoError(t, err)
	_, err = service.PutEntry(ctx, memory.PutEntryInput{Scope: sharedScope, Content: "memory shared", Author: "system", Timestamp: time.Unix(30, 0)})
	require.NoError(t, err)

	userResult, err := service.Search(ctx, memory.SearchQuery{Scope: userScope, Query: "memory", Limit: 1})
	require.NoError(t, err)
	require.True(t, userResult.Truncated)
	require.Len(t, userResult.Hits, 1)
	require.Equal(t, "memory beta", userResult.Hits[0].Entry.Content)

	sharedResult, err := service.Search(ctx, memory.SearchQuery{Scope: sharedScope, Query: "memory"})
	require.NoError(t, err)
	require.Len(t, sharedResult.Hits, 1)
	require.Equal(t, memory.VisibilityShared, sharedResult.Hits[0].Entry.Scope.Visibility)
}

func TestServiceRejectsInvalidScopes(t *testing.T) {
	ctx := context.Background()
	service := NewService()

	_, err := service.PutEntry(ctx, memory.PutEntryInput{
		Scope:   memory.Scope{Visibility: memory.VisibilityUser, UserID: "user-1"},
		Content: "missing namespace",
		Author:  "user",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "namespace is required")

	_, err = service.PutEntry(ctx, memory.PutEntryInput{
		Scope:   memory.Scope{Namespace: "workspace-a", Visibility: memory.VisibilityUser},
		Content: "missing user",
		Author:  "user",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "user id")

	_, err = service.PutEntry(ctx, memory.PutEntryInput{
		Scope:   memory.Scope{Namespace: "workspace-a", UserID: "user-1"},
		Content: "missing visibility",
		Author:  "user",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "visibility is required")

	_, err = service.Search(ctx, memory.SearchQuery{Scope: memory.Scope{}, Query: "anything"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "namespace is required")
}
