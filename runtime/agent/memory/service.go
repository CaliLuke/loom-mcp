package memory

import (
	"context"
	"time"
)

type (
	// Service stores and searches long-term memory entries. It is separate from
	// Store, which persists raw run transcripts.
	Service interface {
		IngestRun(ctx context.Context, input IngestRunInput) (IngestResult, error)
		IngestEvents(ctx context.Context, input IngestEventsInput) (IngestResult, error)
		PutEntry(ctx context.Context, input PutEntryInput) (Entry, error)
		Search(ctx context.Context, query SearchQuery) (SearchResult, error)
	}

	// ScopeResolver derives tenant and user routing for runtime-owned memory calls.
	ScopeResolver interface {
		ResolveMemoryScope(ctx context.Context, input ScopeInput) (Scope, error)
	}

	// ScopeResolverFunc adapts a function to ScopeResolver.
	ScopeResolverFunc func(ctx context.Context, input ScopeInput) (Scope, error)

	// Scope routes durable memory entries and queries.
	Scope struct {
		Namespace  string
		UserID     string
		Visibility Visibility
	}

	// ScopeInput carries runtime context used to resolve a memory scope.
	ScopeInput struct {
		AgentID    string
		SessionID  string
		RunID      string
		Visibility Visibility
		Labels     map[string]string
		Payload    map[string]any
	}

	// Visibility controls whether memory is user-private or explicitly shared.
	Visibility string

	// SourceKind describes where a long-term memory entry came from.
	SourceKind string

	// SourceRef identifies source material used to produce an entry.
	SourceRef struct {
		Kind           SourceKind
		AgentID        string
		SessionID      string
		RunID          string
		EventOrdinal   int
		EventHash      string
		ToolCallID     string
		ExternalID     string
		IdempotencyKey string
	}

	// IngestRunInput asks a service to extract entries from a completed run.
	IngestRunInput struct {
		Scope     Scope
		AgentID   string
		SessionID string
		RunID     string
		Events    []Event
		Labels    map[string]string
		Metadata  map[string]any
	}

	// IngestEventsInput asks a service to extract entries from event deltas.
	IngestEventsInput struct {
		Scope        Scope
		AgentID      string
		SessionID    string
		RunID        string
		StartOrdinal int
		Events       []Event
		Labels       map[string]string
		Metadata     map[string]any
	}

	// PutEntryInput writes a direct long-term memory entry.
	PutEntryInput struct {
		Scope     Scope
		Content   string
		Author    string
		Timestamp time.Time
		Sources   []SourceRef
		Labels    map[string]string
		Metadata  map[string]any
	}

	// IngestResult reports entries written by an ingest call.
	IngestResult struct {
		Entries   []Entry
		Skipped   int
		Truncated bool
	}

	// Entry is durable long-term memory state.
	Entry struct {
		ID        string
		Scope     Scope
		Content   string
		Author    string
		Timestamp time.Time
		Sources   []SourceRef
		Labels    map[string]string
		Metadata  map[string]any
	}

	// SearchQuery filters long-term memory entries.
	SearchQuery struct {
		Scope  Scope
		Query  string
		Labels map[string]string
		Limit  int
	}

	// SearchHit is one query-time match.
	SearchHit struct {
		Entry       Entry
		Score       float64
		Snippet     string
		MatchedRefs []SourceRef
	}

	// SearchResult contains ranked long-term memory hits.
	SearchResult struct {
		Hits      []SearchHit
		Truncated bool
	}

	// ToolSource selects which memory tools a generated memory toolset exposes.
	ToolSource string
)

const (
	// VisibilityUser scopes memory to one resolved user.
	VisibilityUser Visibility = "user"
	// VisibilityShared scopes memory to explicitly shared knowledge.
	VisibilityShared Visibility = "shared"

	// SourceRunEvent identifies entries extracted from transcript events.
	SourceRunEvent SourceKind = "run_event"
	// SourceDirect identifies direct application writes.
	SourceDirect SourceKind = "direct"
	// SourceExternal identifies imported entries from external systems.
	SourceExternal SourceKind = "external"

	// ToolSourceTranscript exposes current-run transcript memory.
	ToolSourceTranscript ToolSource = "transcript"
	// ToolSourceIndexedTranscript exposes indexed raw transcript search.
	ToolSourceIndexedTranscript ToolSource = "indexed_transcript"
	// ToolSourceLongTerm exposes entry-shaped long-term memory search.
	ToolSourceLongTerm ToolSource = "long_term"
)

// ResolveMemoryScope calls f(ctx, input).
func (f ScopeResolverFunc) ResolveMemoryScope(ctx context.Context, input ScopeInput) (Scope, error) {
	return f(ctx, input)
}
