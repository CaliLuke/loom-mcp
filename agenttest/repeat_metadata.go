package agenttest

import (
	"io"
	"strings"
	"sync"
	"unicode"
)

type attemptOutput struct {
	mu      sync.Mutex
	tb      *attemptTB
	pending string
}

// Skipped reports this attempt's skip state, including during cleanup.
func (t *attemptTB) Skipped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.skipped
}

// Output provides line-buffered logs attributed to this attempt.
func (t *attemptTB) Output() io.Writer {
	return &t.output
}

// Attr records attempt metadata in its log instead of changing parent attributes.
func (t *attemptTB) Attr(key, value string) {
	if strings.ContainsFunc(key, unicode.IsSpace) {
		t.Errorf("disallowed whitespace in attribute key %q", key)
		return
	}
	if strings.ContainsAny(value, "\r\n") {
		t.Errorf("disallowed newline in attribute value %q", value)
		return
	}
	t.Logf("%s=%s", key, value)
}

// Write buffers text until complete lines can be logged for the attempt.
func (w *attemptOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending += string(data)
	for {
		line, rest, ok := strings.Cut(w.pending, "\n")
		if !ok {
			break
		}
		w.tb.Log(line)
		w.pending = rest
	}
	return len(data), nil
}

func (w *attemptOutput) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending != "" {
		w.tb.Log(w.pending)
		w.pending = ""
	}
}
