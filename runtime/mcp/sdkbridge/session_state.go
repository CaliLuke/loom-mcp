package sdkbridge

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	mcpruntime "github.com/CaliLuke/loom-mcp/v2/runtime/mcp"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	sessionTTL  = 24 * time.Hour
	maxSessions = 4096
)

var (
	errInvalidSessionID               = errors.New("invalid session ID")
	errSessionPrincipalBindingMissing = errors.New("session principal binding missing")
	errSessionPrincipalMismatch       = errors.New("session user mismatch")
)

// PrincipalResolver returns the stable owner of the current MCP session.
type PrincipalResolver func(context.Context) string

// SessionState owns initialized-session and principal-binding state for an MCP server.
type SessionState struct {
	mu                  sync.RWMutex
	initialized         bool
	initializedSessions map[string]time.Time
	sessionPrincipals   map[string]string
	principal           PrincipalResolver
	now                 func() time.Time
}

// NewSessionState creates bounded session state for one MCP server.
func NewSessionState(principal PrincipalResolver) *SessionState {
	return &SessionState{
		initializedSessions: make(map[string]time.Time),
		sessionPrincipals:   make(map[string]string),
		principal:           principal,
		now:                 time.Now,
	}
}

// IsInitialized reports whether the request belongs to an initialized session.
func (state *SessionState) IsInitialized(ctx context.Context) bool {
	if state == nil {
		return false
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	if sessionID := mcpruntime.SessionIDFromContext(ctx); sessionID != "" {
		_, ok := state.initializedSessions[sessionID]
		return ok
	}
	return state.initialized
}

// MarkInitialized records one initialized SDK session. An empty ID marks stateless initialization.
func (state *SessionState) MarkInitialized(sessionID string) {
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if sessionID == "" {
		state.initialized = true
		return
	}
	now := state.now()
	if _, ok := state.initializedSessions[sessionID]; !ok {
		state.pruneLocked(now, true)
	}
	state.initializedSessions[sessionID] = now
}

// CapturePrincipal binds an initialized session to its current stable principal.
func (state *SessionState) CapturePrincipal(ctx context.Context, sessionID string) {
	if state == nil || sessionID == "" {
		return
	}
	principal := state.resolvePrincipal(ctx)
	if principal == "" {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if _, ok := state.initializedSessions[sessionID]; !ok {
		return
	}
	if strings.TrimSpace(state.sessionPrincipals[sessionID]) != "" {
		return
	}
	state.sessionPrincipals[sessionID] = principal
}

// AssertPrincipal checks that a live session belongs to the current principal.
func (state *SessionState) AssertPrincipal(ctx context.Context, sessionID string) error {
	if state == nil || sessionID == "" {
		return nil
	}
	actual := state.resolvePrincipal(ctx)
	principalRequired := state.principal != nil
	state.mu.Lock()
	state.pruneLocked(state.now(), false)
	_, initialized := state.initializedSessions[sessionID]
	expected := strings.TrimSpace(state.sessionPrincipals[sessionID])
	state.mu.Unlock()
	if !initialized {
		return errInvalidSessionID
	}
	if expected == "" {
		if principalRequired || actual != "" {
			return errSessionPrincipalBindingMissing
		}
		return nil
	}
	if actual == "" || actual != expected {
		return errSessionPrincipalMismatch
	}
	return nil
}

// Clear removes an initialized session and its principal binding.
func (state *SessionState) Clear(sessionID string) {
	if state == nil || sessionID == "" {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	delete(state.initializedSessions, sessionID)
	delete(state.sessionPrincipals, sessionID)
}

// IsInvalidSessionID reports whether err identifies an unknown or expired session.
func IsInvalidSessionID(err error) bool {
	return errors.Is(err, errInvalidSessionID)
}

func (state *SessionState) resolvePrincipal(ctx context.Context) string {
	if state.principal != nil {
		return strings.TrimSpace(state.principal(ctx))
	}
	if tokenInfo := mcpauth.TokenInfoFromContext(ctx); tokenInfo != nil {
		return strings.TrimSpace(tokenInfo.UserID)
	}
	return ""
}

func (state *SessionState) pruneLocked(now time.Time, reserveSlot bool) {
	for sessionID, touchedAt := range state.initializedSessions {
		if now.Sub(touchedAt) >= sessionTTL {
			delete(state.initializedSessions, sessionID)
			delete(state.sessionPrincipals, sessionID)
		}
	}
	for len(state.initializedSessions) > maxSessions || reserveSlot && len(state.initializedSessions) >= maxSessions {
		oldestID := ""
		var oldestAt time.Time
		for sessionID, touchedAt := range state.initializedSessions {
			if oldestID == "" || touchedAt.Before(oldestAt) {
				oldestID = sessionID
				oldestAt = touchedAt
			}
		}
		if oldestID == "" {
			return
		}
		delete(state.initializedSessions, oldestID)
		delete(state.sessionPrincipals, oldestID)
	}
}
