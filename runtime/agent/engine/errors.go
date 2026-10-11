package engine

import "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/errtypes"

// ApplicationError carries a portable activity or workflow failure. Every engine
// must stop retrying an activity that returns this error with NonRetryable set.
// Activity and workflow failures surfaced to workflow code must carry the engine
// error types so errors.As can inspect them in the runtime. Details is one
// serializable value; Cause retains the underlying failure.
type ApplicationError = errtypes.ApplicationError

// TimeoutError marks an engine execution timeout and retains its underlying cause.
// Engines must surface this type to workflow code for execution timeouts.
type TimeoutError = errtypes.TimeoutError

// ErrCanceled marks canceled activities, workflows and timers. It shares
// context.Canceled's identity to preserve existing context cancellation checks.
var ErrCanceled = errtypes.ErrCanceled

// NewApplicationError constructs a retryable application failure.
func NewApplicationError(message, errorType string, details any, cause error) *ApplicationError {
	return errtypes.NewApplicationError(message, errorType, details, cause)
}

// NewNonRetryableApplicationError constructs a failure that must not be retried.
func NewNonRetryableApplicationError(message, errorType string, details any, cause error) *ApplicationError {
	return errtypes.NewNonRetryableApplicationError(message, errorType, details, cause)
}

// IsCanceled reports whether err carries the engine cancellation marker. Mixed
// error graphs require the runtime's exact-leaf classifier to decide run status.
func IsCanceled(err error) bool {
	return errtypes.IsCanceled(err)
}

// IsTimeout reports an engine timeout or a native context deadline.
func IsTimeout(err error) bool {
	return errtypes.IsTimeout(err)
}
