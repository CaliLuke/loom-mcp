package mcp

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestSessionIDFromContext(t *testing.T) {
	ctx := context.Background()
	if got := SessionIDFromContext(ctx); got != "" {
		t.Fatalf("expected empty session id, got %q", got)
	}
	ctx = WithSessionID(ctx, "sess-123")
	if got := SessionIDFromContext(ctx); got != "sess-123" {
		t.Fatalf("expected stored session id, got %q", got)
	}
}

func TestEnsureSessionIDWritesResponseHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	ctx := WithResponseWriter(context.Background(), rec)
	sessionID := EnsureSessionID(ctx)
	if sessionID == "" {
		t.Fatal("expected generated session id")
	}
	if got := rec.Header().Get(HeaderKeySessionID); got != sessionID {
		t.Fatalf("expected session header %q, got %q", sessionID, got)
	}
	if again := EnsureSessionID(ctx); again != sessionID {
		t.Fatalf("expected idempotent session id, got %q want %q", again, sessionID)
	}
}
