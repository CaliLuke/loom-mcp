package inmem

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
)

// AdvanceContinuation implements session.ContinuationStore under the store mutex.
func (s *Store) AdvanceContinuation(ctx context.Context, sessionID string, expected *string, next session.Continuation) (session.AdvanceOutcome, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	body, err := session.EncodeContinuation(next)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.continuations[sessionID][next.ID]; ok {
		if bytes.Equal(old, body) {
			return session.AlreadyPublished, nil
		}
		return session.Conflict, nil
	}
	if _, ok := s.sessions[sessionID]; !ok {
		return "", session.ErrSessionNotFound
	}
	head := s.continuationHeads[sessionID]
	if (expected == nil && head != "") || (expected != nil && *expected != head) {
		return session.Superseded, nil
	}
	if err := s.validateContinuation(sessionID, head, expected, next); err != nil {
		return "", err
	}

	if s.continuations[sessionID] == nil {
		s.continuations[sessionID] = make(map[string][]byte)
	}
	s.continuations[sessionID][next.ID] = body
	s.continuationHeads[sessionID] = next.ID
	return session.Published, nil
}

// LoadContinuationHead returns an owned snapshot, or nil for an empty session.
func (s *Store) LoadContinuationHead(ctx context.Context, sessionID string) (*session.Continuation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.sessions[sessionID]; !ok {
		return nil, session.ErrSessionNotFound
	}
	head := s.continuationHeads[sessionID]
	if head == "" {
		return nil, nil
	}
	var out session.Continuation
	if err := json.Unmarshal(s.continuations[sessionID][head], &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LoadContinuation returns an owned immutable body within one session.
func (s *Store) LoadContinuation(ctx context.Context, sessionID, id string) (session.Continuation, error) {
	if err := ctx.Err(); err != nil {
		return session.Continuation{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	body, ok := s.continuations[sessionID][id]
	if !ok {
		return session.Continuation{}, session.ErrContinuationNotFound
	}
	var out session.Continuation
	err := json.Unmarshal(body, &out)
	return out, err
}

func sameContinuationID(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// validateContinuation runs while the store lock is held.
func (s *Store) validateContinuation(sessionID, head string, expected *string, next session.Continuation) error {
	if next.ID == "" || next.Pos < 1 || !sameContinuationID(next.Prev, expected) {
		return fmt.Errorf("invalid continuation identity")
	}
	position := 1
	if head != "" {
		var previous session.Continuation
		if err := json.Unmarshal(s.continuations[sessionID][head], &previous); err != nil {
			return err
		}
		position = previous.Pos + 1
	}
	if next.Pos != position {
		return fmt.Errorf("continuation position must be %d", position)
	}
	return nil
}
