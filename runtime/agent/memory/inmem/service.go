package inmem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
)

const (
	authorUser      = "user"
	authorAssistant = "assistant"
	authorPlanner   = "planner"
)

// Service implements memory.Service with process-local storage.
type Service struct {
	mu        sync.RWMutex
	nextID    int
	entries   map[string]memory.Entry
	sourceIDs map[string]string
}

// NewService returns an empty in-memory long-term memory service.
func NewService() *Service {
	return &Service{
		entries:   make(map[string]memory.Entry),
		sourceIDs: make(map[string]string),
	}
}

// IngestRun extracts textual entries from a run snapshot.
func (s *Service) IngestRun(ctx context.Context, input memory.IngestRunInput) (memory.IngestResult, error) {
	return s.ingestEvents(ctx, memory.IngestEventsInput{
		Scope:        input.Scope,
		AgentID:      input.AgentID,
		SessionID:    input.SessionID,
		RunID:        input.RunID,
		StartOrdinal: 0,
		Events:       input.Events,
		Labels:       input.Labels,
		Metadata:     input.Metadata,
	})
}

// IngestEvents extracts textual entries from event deltas.
func (s *Service) IngestEvents(ctx context.Context, input memory.IngestEventsInput) (memory.IngestResult, error) {
	return s.ingestEvents(ctx, input)
}

// PutEntry stores a direct long-term memory entry.
func (s *Service) PutEntry(_ context.Context, input memory.PutEntryInput) (memory.Entry, error) {
	entry := memory.Entry{
		Scope:     input.Scope,
		Content:   strings.TrimSpace(input.Content),
		Author:    input.Author,
		Timestamp: input.Timestamp,
		Sources:   cloneSources(input.Sources),
		Labels:    cloneStringMap(input.Labels),
		Metadata:  cloneAnyMap(input.Metadata),
	}
	if err := validateScope(entry.Scope); err != nil {
		return memory.Entry{}, err
	}
	if entry.Content == "" {
		return memory.Entry{}, fmt.Errorf("memory: entry content is required")
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putEntryLocked(entry), nil
}

// Search returns deterministic keyword matches scoped by namespace, user, and labels.
func (s *Service) Search(_ context.Context, query memory.SearchQuery) (memory.SearchResult, error) {
	if err := validateScope(query.Scope); err != nil {
		return memory.SearchResult{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	tokens := tokenize(query.Query)
	hits := make([]memory.SearchHit, 0, len(s.entries))
	for _, entry := range s.entries {
		if !scopeMatches(entry.Scope, query.Scope) || !labelsMatch(entry.Labels, query.Labels) {
			continue
		}
		score := entryScore(entry, tokens)
		if len(tokens) > 0 && score == 0 {
			continue
		}
		hits = append(hits, memory.SearchHit{
			Entry:       cloneEntry(entry),
			Score:       score,
			Snippet:     entry.Content,
			MatchedRefs: cloneSources(entry.Sources),
		})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if !hits[i].Entry.Timestamp.Equal(hits[j].Entry.Timestamp) {
			return hits[i].Entry.Timestamp.After(hits[j].Entry.Timestamp)
		}
		return hits[i].Entry.ID < hits[j].Entry.ID
	})
	truncated := false
	if query.Limit > 0 && len(hits) > query.Limit {
		truncated = true
		hits = hits[:query.Limit]
	}
	return memory.SearchResult{Hits: hits, Truncated: truncated}, nil
}

func validateScope(scope memory.Scope) error {
	if strings.TrimSpace(scope.Namespace) == "" {
		return fmt.Errorf("memory: namespace is required")
	}
	switch scope.Visibility {
	case memory.VisibilityUser:
		if strings.TrimSpace(scope.UserID) == "" {
			return fmt.Errorf("memory: user-scoped memory requires a user id")
		}
	case memory.VisibilityShared:
	case "":
		return fmt.Errorf("memory: visibility is required")
	default:
		return fmt.Errorf("memory: unknown visibility %q", scope.Visibility)
	}
	return nil
}

func (s *Service) ingestEvents(ctx context.Context, input memory.IngestEventsInput) (memory.IngestResult, error) {
	if err := validateScope(input.Scope); err != nil {
		return memory.IngestResult{}, err
	}
	if len(input.Events) == 0 {
		return memory.IngestResult{}, nil
	}
	entries := make([]memory.Entry, 0, len(input.Events))
	skipped := 0
	for idx, event := range input.Events {
		content, author, ok := eventText(event)
		if !ok {
			skipped++
			continue
		}
		source := memory.SourceRef{
			Kind:         memory.SourceRunEvent,
			AgentID:      input.AgentID,
			SessionID:    input.SessionID,
			RunID:        input.RunID,
			EventOrdinal: input.StartOrdinal + idx,
			EventHash:    eventHash(event),
		}
		entry, err := s.PutEntry(ctx, memory.PutEntryInput{
			Scope:     input.Scope,
			Content:   content,
			Author:    author,
			Timestamp: event.Timestamp,
			Sources:   []memory.SourceRef{source},
			Labels:    input.Labels,
			Metadata:  input.Metadata,
		})
		if err != nil {
			return memory.IngestResult{}, err
		}
		entries = append(entries, entry)
	}
	return memory.IngestResult{Entries: entries, Skipped: skipped}, nil
}

func (s *Service) putEntryLocked(entry memory.Entry) memory.Entry {
	for _, source := range entry.Sources {
		key := sourceKey(entry.Scope, source)
		if key == "" {
			continue
		}
		if existingID := s.sourceIDs[key]; existingID != "" {
			return cloneEntry(s.entries[existingID])
		}
	}
	s.nextID++
	entry.ID = fmt.Sprintf("mem-%d", s.nextID)
	stored := cloneEntry(entry)
	s.entries[stored.ID] = stored
	for _, source := range stored.Sources {
		key := sourceKey(stored.Scope, source)
		if key != "" {
			s.sourceIDs[key] = stored.ID
		}
	}
	return cloneEntry(stored)
}

func eventText(event memory.Event) (content, author string, ok bool) {
	switch event.Type {
	case memory.EventUserMessage:
		data, err := memory.DecodeUserMessageData(event)
		return data.Message, authorUser, err == nil && strings.TrimSpace(data.Message) != ""
	case memory.EventAssistantMessage:
		data, err := memory.DecodeAssistantMessageData(event)
		return data.Message, authorAssistant, err == nil && strings.TrimSpace(data.Message) != ""
	case memory.EventPlannerNote:
		data, err := memory.DecodePlannerNoteData(event)
		return data.Note, authorPlanner, err == nil && strings.TrimSpace(data.Note) != ""
	case memory.EventToolCall, memory.EventToolResult, memory.EventThinking:
		return "", "", false
	default:
		return "", "", false
	}
}

func eventHash(event memory.Event) string {
	raw, _ := json.Marshal(event)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func sourceKey(scope memory.Scope, source memory.SourceRef) string {
	if source.IdempotencyKey != "" {
		return strings.Join([]string{scope.Namespace, scope.UserID, string(scope.Visibility), source.IdempotencyKey}, "\x00")
	}
	if source.ExternalID != "" {
		return strings.Join([]string{scope.Namespace, scope.UserID, string(scope.Visibility), string(source.Kind), source.ExternalID}, "\x00")
	}
	if source.Kind == memory.SourceRunEvent && source.AgentID != "" && source.RunID != "" {
		return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%s",
			scope.Namespace, scope.UserID, scope.Visibility, source.Kind, source.AgentID, source.RunID, source.EventOrdinal, source.EventHash)
	}
	return ""
}

func scopeMatches(entry, query memory.Scope) bool {
	if query.Namespace != "" && entry.Namespace != query.Namespace {
		return false
	}
	if query.Visibility != "" && entry.Visibility != query.Visibility {
		return false
	}
	if query.UserID != "" && entry.UserID != query.UserID {
		return false
	}
	return true
}

func labelsMatch(labels, query map[string]string) bool {
	for key, want := range query {
		if labels[key] != want {
			return false
		}
	}
	return true
}

func entryScore(entry memory.Entry, tokens []string) float64 {
	if len(tokens) == 0 {
		return 1
	}
	text := strings.ToLower(entry.Content)
	score := 0
	for _, token := range tokens {
		if strings.Contains(text, token) {
			score++
		}
	}
	return float64(score) / float64(len(tokens))
}

func tokenize(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	out := fields[:0]
	for _, field := range fields {
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func cloneEntry(entry memory.Entry) memory.Entry {
	entry.Sources = cloneSources(entry.Sources)
	entry.Labels = cloneStringMap(entry.Labels)
	entry.Metadata = cloneAnyMap(entry.Metadata)
	return entry
}

func cloneSources(in []memory.SourceRef) []memory.SourceRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]memory.SourceRef, len(in))
	copy(out, in)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
