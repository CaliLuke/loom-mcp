// Package inmem provides an in-memory implementation of session.Store.
//
// It is intended for tests and local development. Production deployments should
// use a durable implementation (for example features/session/mongo).
package inmem

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/internal/structured"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
)

type (
	// Store is an in-memory implementation of session.Store.
	// It is safe for concurrent use.
	Store struct {
		mu            sync.RWMutex
		sessions      map[string]session.Session
		runs          map[string]session.RunMeta
		startAttempts map[string]runStartAttempt
	}

	runStartAttempt struct {
		id          string
		parentRunID string
	}
)

// New returns an empty Store.
func New() *Store {
	return &Store{
		sessions:      make(map[string]session.Session),
		runs:          make(map[string]session.RunMeta),
		startAttempts: make(map[string]runStartAttempt),
	}
}

// CreateSession implements session.Store.
func (s *Store) CreateSession(ctx context.Context, sessionID string, createdAt time.Time) (session.Session, error) {
	if err := ctx.Err(); err != nil {
		return session.Session{}, err
	}
	if sessionID == "" {
		return session.Session{}, errors.New("session id is required")
	}
	if createdAt.IsZero() {
		return session.Session{}, errors.New("created_at is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.sessions[sessionID]
	if ok {
		if existing.Status == session.StatusEnded {
			return session.Session{}, session.ErrSessionEnded
		}
		return cloneSession(existing), nil
	}

	out := session.Session{
		ID:        sessionID,
		Status:    session.StatusActive,
		CreatedAt: createdAt.UTC(),
		EndedAt:   nil,
	}
	s.sessions[sessionID] = out
	return cloneSession(out), nil
}

// LoadSession implements session.Store.
func (s *Store) LoadSession(ctx context.Context, sessionID string) (session.Session, error) {
	if err := ctx.Err(); err != nil {
		return session.Session{}, err
	}
	if sessionID == "" {
		return session.Session{}, errors.New("session id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	existing, ok := s.sessions[sessionID]
	if !ok {
		return session.Session{}, session.ErrSessionNotFound
	}
	return cloneSession(existing), nil
}

// EndSession implements session.Store.
func (s *Store) EndSession(ctx context.Context, sessionID string, endedAt time.Time) (session.Session, error) {
	if err := ctx.Err(); err != nil {
		return session.Session{}, err
	}
	if sessionID == "" {
		return session.Session{}, errors.New("session id is required")
	}
	if endedAt.IsZero() {
		return session.Session{}, errors.New("ended_at is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.sessions[sessionID]
	if !ok {
		return session.Session{}, session.ErrSessionNotFound
	}
	if existing.Status == session.StatusEnded {
		return cloneSession(existing), nil
	}
	at := endedAt.UTC()
	existing.Status = session.StatusEnded
	existing.EndedAt = &at
	s.sessions[sessionID] = existing
	return cloneSession(existing), nil
}

// UpsertRun implements session.Store.
//
// ChildRunIDs is intentionally excluded from the update: parent-child links
// are exclusively managed by LinkChildRun, so callers doing load-modify-write
// (for example runtime hook handlers) can never erase links committed
// concurrently by LinkChildRun. Any RunMeta.ChildRunIDs value passed here is
// ignored and the stored links are preserved.
func (s *Store) UpsertRun(ctx context.Context, run session.RunMeta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if run.RunID == "" {
		return errors.New("run id is required")
	}
	if run.AgentID == "" {
		return errors.New("agent id is required")
	}
	if run.SessionID == "" {
		return errors.New("session id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	existing, exists := s.runs[run.RunID]
	if err := s.validateRunOwnership(run, existing, exists); err != nil {
		return err
	}
	if err := normalizeRunTimestamps(&run, existing, exists, now); err != nil {
		return err
	}
	if session.IsTerminalRunStatus(existing.Status) {
		run.Status = existing.Status
	}
	run.UpdatedAt = now

	// Child links are owned by LinkChildRun; keep the committed links and
	// drop whatever the caller passed.
	run.ChildRunIDs = existing.ChildRunIDs

	s.runs[run.RunID] = cloneRunMeta(run)
	return nil
}

// ReserveRun implements session.Store.
func (s *Store) ReserveRun(ctx context.Context, run session.RunMeta, attemptID, parentRunID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := session.ValidateRunReservation(run, attemptID); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reserveRunLocked(run, attemptID, parentRunID)
}

// reserveRunLocked creates a reservation and optional parent link while s.mu is held.
func (s *Store) reserveRunLocked(run session.RunMeta, attemptID, parentRunID string) error {
	if _, exists := s.runs[run.RunID]; exists {
		owner := s.startAttempts[run.RunID]
		existing := s.runs[run.RunID]
		if owner.id == attemptID && owner.parentRunID == parentRunID &&
			existing.AgentID == run.AgentID && existing.SessionID == run.SessionID {
			return nil
		}
		return session.ErrRunAlreadyExists
	}
	owner, ok := s.sessions[run.SessionID]
	if !ok {
		return session.ErrSessionNotFound
	}
	if owner.Status == session.StatusEnded {
		return session.ErrSessionEnded
	}
	var parent session.RunMeta
	if parentRunID != "" {
		var err error
		parent, err = s.loadReservationParentLocked(parentRunID, run.SessionID)
		if err != nil {
			return err
		}
	}

	s.persistRunReservationLocked(run, attemptID, parentRunID, parent)
	return nil
}

// loadReservationParentLocked validates the parent identity while s.mu is held.
func (s *Store) loadReservationParentLocked(parentRunID, sessionID string) (session.RunMeta, error) {
	parent, ok := s.runs[parentRunID]
	if !ok {
		return session.RunMeta{}, session.ErrRunNotFound
	}
	if parent.SessionID != sessionID {
		return session.RunMeta{}, session.ErrRunSessionMismatch
	}
	return parent, nil
}

// persistRunReservationLocked writes the new run and optional link while s.mu is held.
func (s *Store) persistRunReservationLocked(run session.RunMeta, attemptID, parentRunID string, parent session.RunMeta) {
	now := time.Now().UTC()
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	run.UpdatedAt = now
	run.ChildRunIDs = nil
	s.runs[run.RunID] = cloneRunMeta(run)
	s.startAttempts[run.RunID] = runStartAttempt{id: attemptID, parentRunID: parentRunID}
	if parentRunID != "" {
		parent.UpdatedAt = now
		parent.ChildRunIDs = appendUniqueRunID(parent.ChildRunIDs, run.RunID)
		s.runs[parentRunID] = cloneRunMeta(parent)
	}
}

// RejectRun implements session.Store.
func (s *Store) RejectRun(ctx context.Context, runID, attemptID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if runID == "" || attemptID == "" {
		return errors.New("run id and run start attempt id are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	run, exists := s.runs[runID]
	if !exists || s.startAttempts[runID].id != attemptID || run.Status != session.RunStatusPending {
		return nil
	}
	run.Status = session.RunStatusFailed
	run.UpdatedAt = time.Now().UTC()
	s.runs[runID] = cloneRunMeta(run)
	return nil
}

// LinkChildRun implements session.Store.
//
// Linking is idempotent: the child run ID is appended to the parent's
// ChildRunIDs only if it is not already present, so repeated links never
// duplicate entries.
func (s *Store) LinkChildRun(ctx context.Context, parentRunID string, child session.RunMeta) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := session.ValidateChildRunLink(parentRunID, child); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.linkChildRunLocked(parentRunID, child)
}

// linkChildRunLocked commits a new child link while s.mu is held.
func (s *Store) linkChildRunLocked(parentRunID string, child session.RunMeta) error {
	parent, ok := s.runs[parentRunID]
	if !ok {
		return session.ErrRunNotFound
	}
	if parent.SessionID != child.SessionID {
		return session.ErrRunSessionMismatch
	}
	existingChild, childExists := s.runs[child.RunID]
	if childExists && existingChild.SessionID != parent.SessionID {
		return session.ErrRunSessionMismatch
	}
	if childExists && slices.Contains(parent.ChildRunIDs, child.RunID) {
		return nil
	}
	if err := s.requireActiveSessionLocked(parent.SessionID); err != nil {
		return err
	}

	s.persistChildRunLinkLocked(parent, child, existingChild, childExists)
	return nil
}

// requireActiveSessionLocked rejects new run admission after session end.
func (s *Store) requireActiveSessionLocked(sessionID string) error {
	owner, exists := s.sessions[sessionID]
	if !exists {
		return session.ErrSessionNotFound
	}
	if owner.Status == session.StatusEnded {
		return session.ErrSessionEnded
	}
	return nil
}

// persistChildRunLinkLocked stores the child and parent link while s.mu is held.
func (s *Store) persistChildRunLinkLocked(parent, child, existingChild session.RunMeta, childExists bool) {
	now := time.Now().UTC()
	if childExists {
		existingChild.UpdatedAt = now
		s.runs[child.RunID] = cloneRunMeta(existingChild)
	} else {
		if child.StartedAt.IsZero() {
			child.StartedAt = now
		}
		child.UpdatedAt = now
		// New runs start without child links; links are added via
		// LinkChildRun only, matching the UpsertRun contract.
		child.ChildRunIDs = nil
		s.runs[child.RunID] = cloneRunMeta(child)
	}

	parent.UpdatedAt = now
	parent.ChildRunIDs = appendUniqueRunID(parent.ChildRunIDs, child.RunID)
	s.runs[parent.RunID] = cloneRunMeta(parent)
}

// LoadRun implements session.Store.
func (s *Store) LoadRun(ctx context.Context, runID string) (session.RunMeta, error) {
	if err := ctx.Err(); err != nil {
		return session.RunMeta{}, err
	}
	if runID == "" {
		return session.RunMeta{}, errors.New("run id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.runs[runID]
	if !ok {
		return session.RunMeta{}, session.ErrRunNotFound
	}
	return cloneRunMeta(run), nil
}

// ListRunsBySession implements session.Store.
func (s *Store) ListRunsBySession(ctx context.Context, sessionID string, statuses []session.RunStatus) ([]session.RunMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sessionID == "" {
		return nil, errors.New("session id is required")
	}
	var allowed map[session.RunStatus]struct{}
	if len(statuses) > 0 {
		allowed = make(map[session.RunStatus]struct{}, len(statuses))
		for _, st := range statuses {
			allowed[st] = struct{}{}
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]session.RunMeta, 0, len(s.runs))
	for _, run := range s.runs {
		if run.SessionID != sessionID {
			continue
		}
		if allowed != nil {
			if _, ok := allowed[run.Status]; !ok {
				continue
			}
		}
		out = append(out, cloneRunMeta(run))
	}
	return out, nil
}

func cloneSession(in session.Session) session.Session {
	out := in
	if in.EndedAt != nil {
		at := *in.EndedAt
		out.EndedAt = &at
	}
	return out
}

func cloneRunMeta(in session.RunMeta) session.RunMeta {
	out := in
	out.Labels = maps.Clone(in.Labels)
	if len(in.PromptRefs) > 0 {
		out.PromptRefs = make([]prompt.PromptRef, len(in.PromptRefs))
		copy(out.PromptRefs, in.PromptRefs)
	}
	if len(in.ChildRunIDs) > 0 {
		out.ChildRunIDs = make([]string, len(in.ChildRunIDs))
		copy(out.ChildRunIDs, in.ChildRunIDs)
	}
	out.Metadata = structured.Clone(in.Metadata)
	return out
}

func appendUniqueRunID(runIDs []string, runID string) []string {
	for _, current := range runIDs {
		if current == runID {
			return runIDs
		}
	}
	return append(runIDs, runID)
}

func normalizeRunTimestamps(run *session.RunMeta, existing session.RunMeta, exists bool, now time.Time) error {
	if !exists || existing.StartedAt.IsZero() {
		if run.StartedAt.IsZero() {
			run.StartedAt = now
		}
		return nil
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = existing.StartedAt
		return nil
	}
	if !run.StartedAt.Equal(existing.StartedAt) {
		return errors.New("started_at is immutable")
	}
	return nil
}

func (s *Store) validateRunOwnership(run, existing session.RunMeta, exists bool) error {
	if exists {
		if existing.SessionID != run.SessionID {
			return session.ErrRunSessionImmutable
		}
		return nil
	}
	owner, sessionExists := s.sessions[run.SessionID]
	if !sessionExists {
		return session.ErrSessionNotFound
	}
	if owner.Status == session.StatusEnded {
		return session.ErrSessionEnded
	}
	return nil
}
