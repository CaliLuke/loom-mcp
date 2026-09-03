package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	agentruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
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

func TestPolicyResourceNamesFromContext(t *testing.T) {
	ctx := context.Background()

	if got := AllowedResourceNamesFromContext(ctx); got != "" {
		t.Fatalf("expected empty allowed resource names, got %q", got)
	}
	if got := DeniedResourceNamesFromContext(ctx); got != "" {
		t.Fatalf("expected empty denied resource names, got %q", got)
	}

	ctx = WithAllowedResourceNames(ctx, "documents,notes")
	ctx = WithDeniedResourceNames(ctx, "private")

	if got := AllowedResourceNamesFromContext(ctx); got != "documents,notes" {
		t.Fatalf("expected allowed resource names, got %q", got)
	}
	if got := DeniedResourceNamesFromContext(ctx); got != "private" {
		t.Fatalf("expected denied resource names, got %q", got)
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

func TestRequestHeadersFromContext(t *testing.T) {
	ctx := context.Background()
	if got := RequestHeadersFromContext(ctx); got != nil {
		t.Fatalf("expected no request headers, got %v", got)
	}

	headers := http.Header{"X-Test": []string{"a"}}
	ctx = WithRequestHeaders(ctx, headers)
	headers.Set("X-Test", "b")

	got := RequestHeadersFromContext(ctx)
	if got.Get("X-Test") != "a" {
		t.Fatalf("expected cloned request header, got %q", got.Get("X-Test"))
	}

	got.Set("X-Test", "c")
	again := RequestHeadersFromContext(ctx)
	if again.Get("X-Test") != "a" {
		t.Fatalf("expected cloned headers on read, got %q", again.Get("X-Test"))
	}
}

func TestProjectedToolCallMetaFromContext(t *testing.T) {
	if _, ok := ProjectedToolCallMetaFromContext(context.Background()); ok {
		t.Fatal("expected absent projected tool call metadata")
	}

	want := agentruntime.ToolCallMeta{SessionID: "verified-session", ToolCallID: "call-1"}
	ctx := WithProjectedToolCallMeta(context.Background(), want)
	got, ok := ProjectedToolCallMetaFromContext(ctx)
	if !ok {
		t.Fatal("expected projected tool call metadata")
	}
	if got != want {
		t.Fatalf("projected tool call metadata = %#v, want %#v", got, want)
	}
}
