// Package errtypes owns engine error values without activity or workflow dependencies.
// Applications use the aliases and constructors exposed by package engine.
package errtypes

import (
	"context"
	"errors"
	"fmt"
)

// ApplicationError carries a portable activity or workflow failure. Every engine
// must stop retrying an activity that returns an ApplicationError with
// NonRetryable set. Activity and workflow failures surfaced to workflow code must
// carry these engine types so errors.As can inspect them in the runtime.
// Details is a single serializable value; Cause retains the underlying failure.
type ApplicationError struct {
	Type         string
	Message      string
	NonRetryable bool
	Details      any
	Cause        error
}

// TimeoutError marks an engine execution timeout and retains its underlying cause.
// Engines must surface this type to workflow code for execution timeouts.
type TimeoutError struct {
	Cause error
}

// ErrCanceled marks engine cancellation. It shares context.Canceled's identity so
// existing context cancellation checks and exact-leaf classification keep working.
// Engines must surface this marker for canceled activities, workflows and timers.
var ErrCanceled = context.Canceled

// NewApplicationError constructs a retryable application failure.
func NewApplicationError(message, errorType string, details any, cause error) *ApplicationError {
	return &ApplicationError{Type: errorType, Message: message, Details: details, Cause: cause}
}

// NewNonRetryableApplicationError constructs a failure that must not be retried.
func NewNonRetryableApplicationError(message, errorType string, details any, cause error) *ApplicationError {
	return &ApplicationError{Type: errorType, Message: message, NonRetryable: true, Details: details, Cause: cause}
}

// IsCanceled reports whether err carries the engine cancellation marker. Mixed
// error graphs require the runtime's exact-leaf classifier to decide run status.
func IsCanceled(err error) bool {
	return errors.Is(err, ErrCanceled)
}

// IsTimeout reports an engine timeout or a native context deadline.
func IsTimeout(err error) bool {
	var timeout *TimeoutError
	return errors.As(err, &timeout) || errors.Is(err, context.DeadlineExceeded)
}

// Error returns the application failure message.
func (e *ApplicationError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

// Unwrap returns the underlying application failure.
func (e *ApplicationError) Unwrap() error {
	return e.Cause
}

// Error returns the underlying timeout message, or a stable default.
func (e *TimeoutError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "execution timed out"
}

// Unwrap returns the underlying timeout failure.
func (e *TimeoutError) Unwrap() error {
	return e.Cause
}
