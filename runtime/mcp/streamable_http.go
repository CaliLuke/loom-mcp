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
)

// NewStreamableHTTPSessions creates a store for issued sessions and active
// stream listeners.
func NewStreamableHTTPSessions() *StreamableHTTPSessions {
	return newStreamableHTTPSessions(defaultStreamableHTTPSessionConfig())
}

// Issue records a session ID as valid for future requests.
func (s *StreamableHTTPSessions) Issue(sessionID string) error {
	if s == nil || sessionID == "" {
		return ErrInvalidSessionID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.pruneLocked(now)
	s.issued[sessionID] = streamableHTTPSessionEntry{expiresAt: now.Add(s.cfg.issuedTTL)}
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
	s.pruneLocked(s.now())
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
	now := s.now()
	s.pruneLocked(now)
	if _, ok := s.issued[sessionID]; !ok {
		if _, terminated := s.terminated[sessionID]; terminated {
			s.mu.Unlock()
			return ErrSessionTerminated
		}
		s.mu.Unlock()
		return ErrInvalidSessionID
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
