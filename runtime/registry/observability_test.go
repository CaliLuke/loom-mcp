package registry

import (
	"context"
	"errors"
	"testing"
)

type recordedLog struct {
	level   string
	message string
	keyvals []any
}

type captureLogger struct {
	logs []recordedLog
}

func (l *captureLogger) Debug(_ context.Context, msg string, keyvals ...any) {
	l.logs = append(l.logs, recordedLog{level: "debug", message: msg, keyvals: append([]any(nil), keyvals...)})
}

func (l *captureLogger) Info(_ context.Context, msg string, keyvals ...any) {
	l.logs = append(l.logs, recordedLog{level: "info", message: msg, keyvals: append([]any(nil), keyvals...)})
}

func (l *captureLogger) Warn(_ context.Context, msg string, keyvals ...any) {
	l.logs = append(l.logs, recordedLog{level: "warn", message: msg, keyvals: append([]any(nil), keyvals...)})
}

func (l *captureLogger) Error(_ context.Context, msg string, keyvals ...any) {
	l.logs = append(l.logs, recordedLog{level: "error", message: msg, keyvals: append([]any(nil), keyvals...)})
}

func TestObservabilityLogSearchFailure(t *testing.T) {
	logger := &captureLogger{}
	obs := NewObservability(logger, nil, nil)

	obs.LogSearchFailure(context.Background(), "registry-a", "calendar", errors.New("boom"))

	if len(logger.logs) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(logger.logs))
	}
	entry := logger.logs[0]
	if entry.level != "warn" {
		t.Fatalf("level: got %q want %q", entry.level, "warn")
	}
	if entry.message != "search failed for registry" {
		t.Fatalf("message: got %q want %q", entry.message, "search failed for registry")
	}
	assertKeyvalsContain(t, entry.keyvals, "registry", "registry-a")
	assertKeyvalsContain(t, entry.keyvals, "query", "calendar")
}

func TestObservabilityLogSyncLifecycle(t *testing.T) {
	logger := &captureLogger{}
	obs := NewObservability(logger, nil, nil)

	obs.LogSyncLifecycle(context.Background(), "started")

	if len(logger.logs) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(logger.logs))
	}
	entry := logger.logs[0]
	if entry.level != "info" {
		t.Fatalf("level: got %q want %q", entry.level, "info")
	}
	if entry.message != "registry sync loop state changed" {
		t.Fatalf("message: got %q want %q", entry.message, "registry sync loop state changed")
	}
	assertKeyvalsContain(t, entry.keyvals, "state", "started")
}

func assertKeyvalsContain(t *testing.T, keyvals []any, key string, want any) {
	t.Helper()

	for i := 0; i+1 < len(keyvals); i += 2 {
		k, ok := keyvals[i].(string)
		if !ok || k != key {
			continue
		}
		if keyvals[i+1] != want {
			t.Fatalf("key %q: got %v want %v", key, keyvals[i+1], want)
		}
		return
	}
	t.Fatalf("missing key %q in %v", key, keyvals)
}
