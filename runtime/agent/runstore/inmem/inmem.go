// Package inmem provides a process-local runstore.Store with no content size
// limit. It is intended for tests and development, not durable storage.
package inmem

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"sync"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

type (
	// Store implements runstore.Store using one mutex and owned byte copies.
	Store struct {
		mu     sync.Mutex
		scopes map[string]*scopeData
	}

	scopeData struct {
		content  map[runstore.Hash][]byte
		calls    map[runstore.CallKey]runstore.CallRecord
		counters map[string]int
	}
)

// New returns an empty in-memory run store.
func New() *Store {
	return &Store{scopes: make(map[string]*scopeData)}
}

// Put implements runstore.Store.
func (s *Store) Put(_ context.Context, scope string, data []byte) (runstore.Hash, error) {
	if scope == "" {
		return "", fmt.Errorf("runstore put: scope is required")
	}
	hash := runstore.Hash(fmt.Sprintf("sha256:%x", sha256.Sum256(data)))
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.scope(scope)
	if _, exists := stored.content[hash]; !exists {
		stored.content[hash] = bytes.Clone(data)
	}
	return hash, nil
}

// Get implements runstore.Store.
func (s *Store) Get(_ context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	if scope == "" {
		return nil, fmt.Errorf("runstore get: scope is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if stored := s.scopes[scope]; stored != nil {
		if data, exists := stored.content[hash]; exists {
			return bytes.Clone(data), nil
		}
	}
	return nil, fmt.Errorf("runstore get scope %q hash %q: %w", scope, hash, runstore.ErrNotFound)
}

// NextSeq implements runstore.Store.
func (s *Store) NextSeq(_ context.Context, scope, runID string) (int, error) {
	if scope == "" {
		return 0, fmt.Errorf("runstore next sequence: scope is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.scope(scope)
	stored.counters[runID]++
	return stored.counters[runID], nil
}

// PutCall implements runstore.Store.
func (s *Store) PutCall(_ context.Context, scope string, record runstore.CallRecord) error {
	if scope == "" {
		return fmt.Errorf("runstore put call: scope is required")
	}
	if err := record.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := s.scope(scope)
	existing, exists := stored.calls[record.Key]
	if err := checkTransition(existing, exists, record); err != nil {
		return fmt.Errorf("runstore put call scope %q key %+v: %w", scope, record.Key, err)
	}
	stored.calls[record.Key] = cloneCall(record)
	return nil
}

// ListCalls implements runstore.Store.
func (s *Store) ListCalls(_ context.Context, scope, runID string) ([]runstore.CallRecord, error) {
	if scope == "" {
		return nil, fmt.Errorf("runstore list calls: scope is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var records []runstore.CallRecord
	if stored := s.scopes[scope]; stored != nil {
		for key, record := range stored.calls {
			if key.RunID == runID {
				records = append(records, cloneCall(record))
			}
		}
	}
	slices.SortFunc(records, func(a, b runstore.CallRecord) int {
		if order := cmp.Compare(a.Key.Seq, b.Key.Seq); order != 0 {
			return order
		}
		return cmp.Compare(a.Key.Attempt, b.Key.Attempt)
	})
	return records, nil
}

// DeleteScope implements runstore.Store.
func (s *Store) DeleteScope(_ context.Context, scope string) error {
	if scope == "" {
		return fmt.Errorf("runstore delete: scope is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.scopes, scope)
	return nil
}

func cloneCall(record runstore.CallRecord) runstore.CallRecord {
	record.Started = bytes.Clone(record.Started)
	record.Completion = bytes.Clone(record.Completion)
	return record
}

// checkTransition validates an atomic call write against its existing record.
func checkTransition(existing runstore.CallRecord, exists bool, next runstore.CallRecord) error {
	if !exists {
		if next.State != runstore.CallStarted {
			return runstore.ErrCallTransition
		}
		return nil
	}
	if next.State == runstore.CallStarted && existing.State != runstore.CallStarted {
		return runstore.ErrCallTransition
	}
	if !bytes.Equal(existing.Started, next.Started) {
		return runstore.ErrCallConflict
	}
	if existing.State != runstore.CallStarted &&
		(existing.State != next.State || !bytes.Equal(existing.Completion, next.Completion)) {
		return runstore.ErrCallConflict
	}
	return nil
}

// scope allocates a scope for a write; the caller must hold mu.
func (s *Store) scope(scope string) *scopeData {
	stored := s.scopes[scope]
	if stored == nil {
		stored = &scopeData{
			content:  make(map[runstore.Hash][]byte),
			calls:    make(map[runstore.CallKey]runstore.CallRecord),
			counters: make(map[string]int),
		}
		s.scopes[scope] = stored
	}
	return stored
}
