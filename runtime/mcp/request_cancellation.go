package mcp

import (
	"context"
	"sync"
)

// RequestCancellationRegistry tracks cancel functions for in-flight MCP
// requests, scoped by session and JSON-RPC request ID.
type RequestCancellationRegistry struct {
	mu       sync.Mutex
	requests map[requestCancellationKey]*requestCancellation
}

type requestCancellationKey struct {
	sessionID string
	requestID string
}

type requestCancellation struct {
	cancel context.CancelFunc
}

// NewRequestCancellationRegistry creates an empty request cancellation registry.
func NewRequestCancellationRegistry() *RequestCancellationRegistry {
	return &RequestCancellationRegistry{
		requests: make(map[requestCancellationKey]*requestCancellation),
	}
}

// Register associates an in-flight request with its cancel function. The
// returned cleanup removes only this registration, so a reused request ID
// cannot be removed by an older request finishing later.
func (r *RequestCancellationRegistry) Register(sessionID string, requestID string, cancel context.CancelFunc) func() {
	if r == nil || sessionID == "" || requestID == "" || cancel == nil {
		panic("MCP request cancellation registration requires a registry, session ID, request ID, and cancel function")
	}

	key := requestCancellationKey{sessionID: sessionID, requestID: requestID}
	entry := &requestCancellation{cancel: cancel}

	r.mu.Lock()
	previous := r.requests[key]
	r.requests[key] = entry
	r.mu.Unlock()

	if previous != nil {
		previous.cancel()
	}

	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.requests[key] == entry {
			delete(r.requests, key)
		}
	}
}

// Cancel cancels and removes the matching in-flight request. It reports
// whether a matching request was registered.
func (r *RequestCancellationRegistry) Cancel(sessionID string, requestID string) bool {
	if r == nil || sessionID == "" || requestID == "" {
		return false
	}

	key := requestCancellationKey{sessionID: sessionID, requestID: requestID}
	r.mu.Lock()
	entry := r.requests[key]
	delete(r.requests, key)
	r.mu.Unlock()

	if entry == nil {
		return false
	}
	entry.cancel()
	return true
}
