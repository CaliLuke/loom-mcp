package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
)

// HeaderKeySessionID is the MCP streamable HTTP session header.
const HeaderKeySessionID = "Mcp-Session-Id"

type (
	sessionIDKey      struct{}
	responseWriterKey struct{}
)

// WithSessionID stores the MCP session ID in ctx.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, sessionID)
}

// SessionIDFromContext returns the MCP session ID stored in ctx.
func SessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v := ctx.Value(sessionIDKey{}); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// WithResponseWriter stores the active HTTP response writer in ctx.
func WithResponseWriter(ctx context.Context, w http.ResponseWriter) context.Context {
	return context.WithValue(ctx, responseWriterKey{}, w)
}

// ResponseWriterFromContext returns the active HTTP response writer from ctx.
func ResponseWriterFromContext(ctx context.Context) http.ResponseWriter {
	if ctx == nil {
		return nil
	}
	if v := ctx.Value(responseWriterKey{}); v != nil {
		if w, ok := v.(http.ResponseWriter); ok {
			return w
		}
	}
	return nil
}

// EnsureSessionID returns the existing session ID from ctx or creates a new one.
// When a response writer is present in ctx, the created session ID is emitted on
// the MCP session header.
func EnsureSessionID(ctx context.Context) string {
	if sessionID := SessionIDFromContext(ctx); sessionID != "" {
		return sessionID
	}
	if w := ResponseWriterFromContext(ctx); w != nil {
		if sessionID := w.Header().Get(HeaderKeySessionID); sessionID != "" {
			return sessionID
		}
		sessionID := NewSessionID()
		w.Header().Set(HeaderKeySessionID, sessionID)
		return sessionID
	}
	return NewSessionID()
}

// NewSessionID generates a transport-agnostic MCP session identifier.
func NewSessionID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(fmt.Errorf("generate mcp session id: %w", err))
	}
	return hex.EncodeToString(buf[:])
}
