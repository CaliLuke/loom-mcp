// Package artifact defines runtime contracts for storing and loading run
// artifacts produced by tools.
package artifact

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type (
	// Store persists artifacts by agent and run. Implementations must be safe for
	// concurrent use and must isolate artifacts across AgentID plus RunID.
	Store interface {
		Save(ctx context.Context, input SaveInput) (Ref, error)
		List(ctx context.Context, query ListQuery) ([]Ref, error)
		Load(ctx context.Context, query LoadQuery) (Content, error)
	}

	// Ref is the workflow-safe artifact reference carried through planner, hook,
	// and API boundaries. It intentionally contains no artifact body.
	Ref struct {
		ID         string            `json:"id"`
		AgentID    string            `json:"agent_id"`
		RunID      string            `json:"run_id"`
		ToolCallID string            `json:"tool_call_id,omitempty"`
		Name       string            `json:"name,omitempty"`
		MimeType   string            `json:"mime_type,omitempty"`
		SizeBytes  int64             `json:"size_bytes"`
		Metadata   map[string]string `json:"metadata,omitempty"`
		CreatedAt  time.Time         `json:"created_at,omitempty"`
	}

	// Content carries an artifact body inside the process. Runtime code converts
	// Content to Ref before crossing workflow or hook boundaries.
	Content struct {
		Ref       Ref
		Body      []byte
		Truncated bool
		SizeBytes int64
	}

	// SaveInput describes one artifact to persist.
	SaveInput struct {
		AgentID    string
		RunID      string
		ToolCallID string
		Name       string
		MimeType   string
		Metadata   map[string]string
		Body       []byte
	}

	// ListQuery filters artifact refs within one agent/run scope.
	ListQuery struct {
		AgentID  string
		RunID    string
		MimeType string
		Metadata map[string]string
		Limit    int
	}

	// LoadQuery selects one artifact body and optionally caps returned bytes.
	LoadQuery struct {
		AgentID  string
		RunID    string
		ID       string
		MaxBytes int
	}

	// MemoryStore is an in-process artifact store for tests and local
	// development. Data is lost when the process exits.
	MemoryStore struct {
		mu   sync.RWMutex
		runs map[string]map[string]map[string]storedArtifact
	}

	storedArtifact struct {
		ref  Ref
		body []byte
	}
)

// ErrNotFound reports that an artifact does not exist in the requested
// agent/run scope.
var ErrNotFound = errors.New("artifact not found")

// NewMemoryStore returns an empty in-memory artifact store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{runs: make(map[string]map[string]map[string]storedArtifact)}
}

// Save stores an artifact and returns its workflow-safe reference.
func (s *MemoryStore) Save(_ context.Context, input SaveInput) (Ref, error) {
	if input.AgentID == "" {
		return Ref{}, fmt.Errorf("artifact save: agent_id is required")
	}
	if input.RunID == "" {
		return Ref{}, fmt.Errorf("artifact save: run_id is required")
	}
	ref := Ref{
		ID:         uuid.NewString(),
		AgentID:    input.AgentID,
		RunID:      input.RunID,
		ToolCallID: input.ToolCallID,
		Name:       input.Name,
		MimeType:   input.MimeType,
		SizeBytes:  int64(len(input.Body)),
		Metadata:   cloneMetadata(input.Metadata),
		CreatedAt:  time.Now().UTC(),
	}
	body := append([]byte(nil), input.Body...)

	s.mu.Lock()
	defer s.mu.Unlock()
	byAgent := s.runs[input.AgentID]
	if byAgent == nil {
		byAgent = make(map[string]map[string]storedArtifact)
		s.runs[input.AgentID] = byAgent
	}
	byRun := byAgent[input.RunID]
	if byRun == nil {
		byRun = make(map[string]storedArtifact)
		byAgent[input.RunID] = byRun
	}
	byRun[ref.ID] = storedArtifact{ref: ref, body: body}
	return cloneRef(ref), nil
}

// List returns artifact refs matching query filters in creation order.
func (s *MemoryStore) List(_ context.Context, query ListQuery) ([]Ref, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byRun := s.runs[query.AgentID][query.RunID]
	if len(byRun) == 0 {
		return nil, nil
	}
	refs := make([]Ref, 0, len(byRun))
	for _, item := range byRun {
		if query.MimeType != "" && item.ref.MimeType != query.MimeType {
			continue
		}
		if !metadataMatches(item.ref.Metadata, query.Metadata) {
			continue
		}
		refs = append(refs, cloneRef(item.ref))
	}
	sortRefs(refs)
	if query.Limit > 0 && len(refs) > query.Limit {
		refs = refs[:query.Limit]
	}
	return refs, nil
}

// Load returns an artifact body from the requested agent/run scope.
func (s *MemoryStore) Load(_ context.Context, query LoadQuery) (Content, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.runs[query.AgentID][query.RunID][query.ID]
	if !ok {
		return Content{}, ErrNotFound
	}
	body := append([]byte(nil), item.body...)
	truncated := false
	if query.MaxBytes > 0 && len(body) > query.MaxBytes {
		body = body[:query.MaxBytes]
		truncated = true
	}
	return Content{
		Ref:       cloneRef(item.ref),
		Body:      body,
		Truncated: truncated,
		SizeBytes: int64(len(item.body)),
	}, nil
}

func sortRefs(refs []Ref) {
	for i := 1; i < len(refs); i++ {
		for j := i; j > 0 && refLess(refs[j], refs[j-1]); j-- {
			refs[j], refs[j-1] = refs[j-1], refs[j]
		}
	}
}

func refLess(left, right Ref) bool {
	if !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.Before(right.CreatedAt)
	}
	return left.ID < right.ID
}

func metadataMatches(refMeta, query map[string]string) bool {
	for key, want := range query {
		if refMeta[key] != want {
			return false
		}
	}
	return true
}

func cloneRef(ref Ref) Ref {
	ref.Metadata = cloneMetadata(ref.Metadata)
	return ref
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}
