package mcp

import (
	"context"
	"errors"
	"sync"
	"time"
)

// StreamableHTTPSessions tracks issued MCP session IDs and active long-lived
// listeners for generated streamable HTTP transports.
type StreamableHTTPSessions struct {
	mu         sync.RWMutex
	issued     map[string]streamableHTTPSessionEntry
	terminated map[string]streamableHTTPSessionEntry
	listeners  map[string]map[*streamListener]struct{}
	cfg        streamableHTTPSessionConfig
}

type streamableHTTPSessionConfig struct {
	issuedTTL     time.Duration
	terminatedTTL time.Duration
	maxIssued     int
	maxTerminated int
	now           func() time.Time
}

type streamableHTTPSessionEntry struct {
	expiresAt time.Time
	principal string
}

type streamListener struct {
	cancel context.CancelFunc
}

const (
	defaultStreamableHTTPSessionIssuedTTL     = 24 * time.Hour
	defaultStreamableHTTPSessionTerminatedTTL = 5 * time.Minute
	defaultStreamableHTTPSessionMaxIssued     = 4096
	defaultStreamableHTTPSessionMaxTerminated = 4096
)

var (
	ErrInvalidSessionID  = errors.New("invalid session ID")
	ErrSessionTerminated = errors.New("session terminated")
	// ErrSessionPrincipalBindingMissing means an authenticated request tried to
	// use a session that was issued without an authenticated principal.
	ErrSessionPrincipalBindingMissing = errors.New("session principal binding missing")
	// ErrSessionPrincipalMismatch means a request principal does not own the
	// referenced session.
	ErrSessionPrincipalMismatch = errors.New("session user mismatch")
)

// NewStreamableHTTPSessions creates a store for issued sessions and active
// stream listeners.
func NewStreamableHTTPSessions() *StreamableHTTPSessions {
	return newStreamableHTTPSessions(defaultStreamableHTTPSessionConfig())
}

// Issue records a session ID as valid for future requests.
func (s *StreamableHTTPSessions) Issue(sessionID string) error {
	return s.IssueForPrincipal(sessionID, "")
}

// IssueForPrincipal records a session ID and its authenticated owner. An empty
// principal deliberately creates an anonymous session that cannot later be
// adopted by an authenticated request.
func (s *StreamableHTTPSessions) IssueForPrincipal(sessionID, principal string) error {
	if s == nil || sessionID == "" {
		return ErrInvalidSessionID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.pruneLocked(now)
	s.issued[sessionID] = streamableHTTPSessionEntry{
		expiresAt: now.Add(s.cfg.issuedTTL),
		principal: principal,
	}
	delete(s.terminated, sessionID)
	s.pruneToMaxLocked(s.issued, s.cfg.maxIssued, s.listeners, sessionID)
	return nil
}

// HasIssued reports whether the store has any active issued sessions.
func (s *StreamableHTTPSessions) HasIssued() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	return len(s.issued) > 0
}

// Validate reports whether a session is currently valid.
func (s *StreamableHTTPSessions) Validate(sessionID string) error {
	if s == nil || sessionID == "" {
		return ErrInvalidSessionID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	return s.validateLocked(sessionID, "", false)
}

// ValidateForPrincipal reports whether a session is valid and owned by
// principal. Anonymous sessions accept only anonymous requests.
func (s *StreamableHTTPSessions) ValidateForPrincipal(sessionID, principal string) error {
	if s == nil || sessionID == "" {
		return ErrInvalidSessionID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	return s.validateLocked(sessionID, principal, true)
}

// RegisterListener atomically validates a session and associates a cancelable stream with it.
func (s *StreamableHTTPSessions) RegisterListener(sessionID string, cancel context.CancelFunc) (func(), error) {
	return s.registerListener(sessionID, "", cancel, false)
}

// RegisterListenerForPrincipal atomically validates session ownership and
// associates a cancelable stream with the session.
func (s *StreamableHTTPSessions) RegisterListenerForPrincipal(sessionID, principal string, cancel context.CancelFunc) (func(), error) {
	return s.registerListener(sessionID, principal, cancel, true)
}

func (s *StreamableHTTPSessions) registerListener(sessionID, principal string, cancel context.CancelFunc, checkPrincipal bool) (func(), error) {
	if s == nil || sessionID == "" || cancel == nil {
		return nil, ErrInvalidSessionID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	if err := s.validateLocked(sessionID, principal, checkPrincipal); err != nil {
		return nil, err
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
	return s.terminate(sessionID, "", false)
}

// TerminateForPrincipal atomically validates session ownership before
// terminating it, preventing a rejected caller from deleting another
// principal's session.
func (s *StreamableHTTPSessions) TerminateForPrincipal(sessionID, principal string) error {
	return s.terminate(sessionID, principal, true)
}

func (s *StreamableHTTPSessions) terminate(sessionID, principal string, checkPrincipal bool) error {
	if s == nil || sessionID == "" {
		return ErrInvalidSessionID
	}
	s.mu.Lock()
	now := s.now()
	s.pruneLocked(now)
	if err := s.validateLocked(sessionID, principal, checkPrincipal); err != nil {
		s.mu.Unlock()
		return err
	}
	s.terminated[sessionID] = streamableHTTPSessionEntry{expiresAt: now.Add(s.cfg.terminatedTTL)}
	delete(s.issued, sessionID)
	listeners := s.listeners[sessionID]
	delete(s.listeners, sessionID)
	s.pruneToMaxLocked(s.terminated, s.cfg.maxTerminated, nil, "")
	s.mu.Unlock()

	for listener := range listeners {
		if listener != nil && listener.cancel != nil {
			listener.cancel()
		}
	}
	return nil
}

func (s *StreamableHTTPSessions) validateLocked(sessionID, principal string, checkPrincipal bool) error {
	if _, ok := s.terminated[sessionID]; ok {
		return ErrSessionTerminated
	}
	entry, ok := s.issued[sessionID]
	if !ok {
		return ErrInvalidSessionID
	}
	if !checkPrincipal {
		return nil
	}
	if entry.principal == "" {
		if principal != "" {
			return ErrSessionPrincipalBindingMissing
		}
		return nil
	}
	if principal != entry.principal {
		return ErrSessionPrincipalMismatch
	}
	return nil
}

func newStreamableHTTPSessions(cfg streamableHTTPSessionConfig) *StreamableHTTPSessions {
	cfg = normalizeStreamableHTTPSessionConfig(cfg)
	return &StreamableHTTPSessions{
		issued:     make(map[string]streamableHTTPSessionEntry),
		terminated: make(map[string]streamableHTTPSessionEntry),
		listeners:  make(map[string]map[*streamListener]struct{}),
		cfg:        cfg,
	}
}

func defaultStreamableHTTPSessionConfig() streamableHTTPSessionConfig {
	return streamableHTTPSessionConfig{
		issuedTTL:     defaultStreamableHTTPSessionIssuedTTL,
		terminatedTTL: defaultStreamableHTTPSessionTerminatedTTL,
		maxIssued:     defaultStreamableHTTPSessionMaxIssued,
		maxTerminated: defaultStreamableHTTPSessionMaxTerminated,
		now:           time.Now,
	}
}

func normalizeStreamableHTTPSessionConfig(cfg streamableHTTPSessionConfig) streamableHTTPSessionConfig {
	defaults := defaultStreamableHTTPSessionConfig()
	if cfg.issuedTTL <= 0 {
		cfg.issuedTTL = defaults.issuedTTL
	}
	if cfg.terminatedTTL <= 0 {
		cfg.terminatedTTL = defaults.terminatedTTL
	}
	if cfg.maxIssued <= 0 {
		cfg.maxIssued = defaults.maxIssued
	}
	if cfg.maxTerminated <= 0 {
		cfg.maxTerminated = defaults.maxTerminated
	}
	if cfg.now == nil {
		cfg.now = defaults.now
	}
	return cfg
}

func (s *StreamableHTTPSessions) now() time.Time {
	return s.cfg.now()
}

func (s *StreamableHTTPSessions) pruneLocked(now time.Time) {
	s.pruneExpiredLocked(s.issued, now, s.listeners)
	s.pruneExpiredLocked(s.terminated, now, nil)
}

func (s *StreamableHTTPSessions) pruneExpiredLocked(entries map[string]streamableHTTPSessionEntry, now time.Time, protected map[string]map[*streamListener]struct{}) {
	for sessionID, entry := range entries {
		if len(protected[sessionID]) > 0 {
			continue
		}
		if !entry.expiresAt.After(now) {
			delete(entries, sessionID)
		}
	}
}

func (s *StreamableHTTPSessions) pruneToMaxLocked(entries map[string]streamableHTTPSessionEntry, max int, protected map[string]map[*streamListener]struct{}, preserveID string) {
	for len(entries) > max {
		var oldestID string
		var oldestExpiresAt time.Time
		for sessionID, entry := range entries {
			if sessionID == preserveID || len(protected[sessionID]) > 0 {
				continue
			}
			if oldestID == "" || entry.expiresAt.Before(oldestExpiresAt) {
				oldestID = sessionID
				oldestExpiresAt = entry.expiresAt
			}
		}
		if oldestID == "" {
			for sessionID, entry := range entries {
				if sessionID == preserveID {
					continue
				}
				if oldestID == "" || entry.expiresAt.Before(oldestExpiresAt) {
					oldestID = sessionID
					oldestExpiresAt = entry.expiresAt
				}
			}
		}
		if oldestID == "" {
			return
		}
		delete(entries, oldestID)
		for listener := range protected[oldestID] {
			if listener != nil && listener.cancel != nil {
				listener.cancel()
			}
		}
		delete(protected, oldestID)
	}
}
