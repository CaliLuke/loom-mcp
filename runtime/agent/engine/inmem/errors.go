package inmem

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/internal/cancellation"
)

// normalizeError marks native deadlines at execution boundaries. Cancellation
// already shares engine.ErrCanceled's identity, and ApplicationError passes
// through unchanged. This engine never retries, including non-retryable failures.
func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	var timeout *engine.TimeoutError
	inspection := cancellation.Inspect(err, func(candidate error, _ string) bool {
		//nolint:errorlint // Inspect supplies each exact graph node.
		if typed, ok := candidate.(*engine.TimeoutError); ok {
			timeout = typed
		}
		return cancellation.Exact(candidate, context.DeadlineExceeded)
	})
	if inspection.Valid && inspection.Matched && timeout == nil {
		return &engine.TimeoutError{Cause: err}
	}
	return err
}
