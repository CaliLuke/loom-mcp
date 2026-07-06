package telemetry

import (
	"context"
	"errors"

	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
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
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	code := grpcStatus.Code(err)
	return code == grpcCodes.Canceled || code == grpcCodes.DeadlineExceeded
}
