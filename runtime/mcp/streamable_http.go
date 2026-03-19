package mcp

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrInvalidSessionID  = errors.New("invalid session ID")
	ErrSessionTerminated = errors.New("session terminated")
)

// StreamableHTTPSessions tracks issued MCP session IDs and active long-lived
// listeners for generated streamable HTTP transports.
type StreamableHTTPSessions struct {
	mu         sync.RWMutex
	issued     map[string]struct{}
	terminated map[string]struct{}
	listeners  map[string]map[*streamListener]struct{}
}

type streamListener struct {
	cancel context.CancelFunc
}

// NewStreamableHTTPSessions creates a store for issued sessions and active
// stream listeners.
func NewStreamableHTTPSessions() *StreamableHTTPSessions {
	return &StreamableHTTPSessions{
		issued:     make(map[string]struct{}),
		terminated: make(map[string]struct{}),
		listeners:  make(map[string]map[*streamListener]struct{}),
	}
}

// Issue records a session ID as valid for future requests.
func (s *StreamableHTTPSessions) Issue(sessionID string) {
	if s == nil || sessionID == "" {
		panic("streamable HTTP session issue requires non-empty session ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issued[sessionID] = struct{}{}
	delete(s.terminated, sessionID)
}

// HasIssued reports whether the store has any active issued sessions.
func (s *StreamableHTTPSessions) HasIssued() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.issued) > 0
}

// Validate reports whether a session is currently valid.
func (s *StreamableHTTPSessions) Validate(sessionID string) error {
	if s == nil || sessionID == "" {
		return ErrInvalidSessionID
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.terminated[sessionID]; ok {
		return ErrSessionTerminated
	}
	if _, ok := s.issued[sessionID]; !ok {
		return ErrInvalidSessionID
	}
	return nil
}

// RegisterListener atomically validates a session and associates a cancelable stream with it.
func (s *StreamableHTTPSessions) RegisterListener(sessionID string, cancel context.CancelFunc) (func(), error) {
	if s == nil || sessionID == "" || cancel == nil {
		return nil, ErrInvalidSessionID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.terminated[sessionID]; ok {
		return nil, ErrSessionTerminated
	}
	if _, ok := s.issued[sessionID]; !ok {
		return nil, ErrInvalidSessionID
	}
	listener := &streamListener{cancel: cancel}
	if s.listeners[sessionID] == nil {
		s.listeners[sessionID] = make(map[*streamListener]struct{})
	}
	s.listeners[sessionID][listener] = struct{}{}
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if listeners := s.listeners[sessionID]; listeners != nil {
			delete(listeners, listener)
			if len(listeners) == 0 {
				delete(s.listeners, sessionID)
			}
		}
	}, nil
}

// Terminate marks a session as terminated and cancels any active listeners.
func (s *StreamableHTTPSessions) Terminate(sessionID string) error {
	if s == nil || sessionID == "" {
		return ErrInvalidSessionID
	}
	s.mu.Lock()
	if _, ok := s.issued[sessionID]; !ok {
		if _, terminated := s.terminated[sessionID]; terminated {
			s.mu.Unlock()
			return ErrSessionTerminated
		}
		s.mu.Unlock()
		return ErrInvalidSessionID
	}
	s.terminated[sessionID] = struct{}{}
	delete(s.issued, sessionID)
	listeners := s.listeners[sessionID]
	delete(s.listeners, sessionID)
	s.mu.Unlock()

	for listener := range listeners {
		if listener != nil && listener.cancel != nil {
			listener.cancel()
		}
	}
	return nil
}
