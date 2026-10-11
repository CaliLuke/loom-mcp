package agenttest

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
)

// ArgMatch checks one decoded payload. Nil means a match. Matchers run only at
// dispatch, under the stub table lock; they must not call harness/stub methods.
type ArgMatch[P any] func(P) error

// When restricts this response to calls whose matchers all pass. The first
// available matching response wins; identical parallel calls race for FIFO slots.
func (r *Response[P, R]) When(matches ...ArgMatch[P]) *Response[P, R] {
	r.stub.h.stubs.mu.Lock()
	r.matches = append(r.matches, matches...)
	r.stub.h.stubs.mu.Unlock()
	return r
}

// Times sets a positive consumption count, replacing Always. The default is one.
func (r *Response[P, R]) Times(n int) *Response[P, R] {
	if n <= 0 {
		r.stub.h.stubSetupError(fmt.Errorf("agenttest: tool %q Times must be positive", r.stub.tool.Name))
		return r
	}
	r.stub.h.stubs.mu.Lock()
	r.remaining, r.always = n, false
	r.stub.h.stubs.mu.Unlock()
	return r
}

// Always makes the response reusable without an unused-response cleanup error.
func (r *Response[P, R]) Always() *Response[P, R] {
	r.stub.h.stubs.mu.Lock()
	r.always = true
	r.stub.h.stubs.mu.Unlock()
	return r
}

// Bounds supplies metadata for a successful bounded result. Pointer fields are
// copied now and per invocation; error responses carry no bounds.
func (r *Response[P, R]) Bounds(bounds agent.Bounds) *Response[P, R] {
	r.stub.h.stubs.mu.Lock()
	r.bounds = copyStubBounds(&bounds)
	r.stub.h.stubs.mu.Unlock()
	return r
}

// After delays execution of this response and respects context cancellation.
func (r *Response[P, R]) After(delay time.Duration) *Response[P, R] {
	if delay < 0 {
		r.stub.h.stubSetupError(fmt.Errorf("agenttest: tool %q After must not be negative", r.stub.tool.Name))
		return r
	}
	r.stub.h.stubs.mu.Lock()
	r.delay = delay
	r.stub.h.stubs.mu.Unlock()
	return r
}

func copyStubBounds(bounds *agent.Bounds) *agent.Bounds {
	if bounds == nil {
		return nil
	}
	copied := *bounds
	if bounds.Total != nil {
		total := *bounds.Total
		copied.Total = &total
	}
	if bounds.NextCursor != nil {
		cursor := *bounds.NextCursor
		copied.NextCursor = &cursor
	}
	return &copied
}

// selectResponse consumes the first available match, reporting each available
// response's first matcher failure if none match. Caller holds the table lock.
func (s *ToolStub[P, R]) selectResponse(payload P) (int, *Response[P, R], error) {
	var failures []string
	for i, response := range s.responses {
		if !response.always && response.remaining == 0 {
			continue
		}
		if err := matchStubArgs(response.matches, payload); err != nil {
			failures = append(failures, fmt.Sprintf("response %d: %s", i+1, err))
			continue
		}
		if !response.always {
			response.remaining--
		}
		selected := *response
		selected.matches = slices.Clone(response.matches)
		selected.bounds = copyStubBounds(response.bounds)
		return i + 1, &selected, nil
	}
	if len(failures) == 0 {
		return 0, nil, ErrStubExhausted
	}
	return 0, nil, fmt.Errorf("unmatched call: %s", strings.Join(failures, "; "))
}
