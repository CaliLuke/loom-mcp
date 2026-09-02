package telemetry

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/internal/cancellation"
)

// ShouldRecordSpanError reports whether err should mark the current span as a
// failure. Context cancellation and deadline errors are suppressed only when
// the active context is already done.
func ShouldRecordSpanError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	return !isContextTerminationError(ctx, err)
}

func isContextTerminationError(ctx context.Context, err error) bool {
	if ctx == nil || err == nil {
		return false
	}
	if ctx.Err() == nil {
		return false
	}
	return cancellation.OnlyContextTermination(err)
}
