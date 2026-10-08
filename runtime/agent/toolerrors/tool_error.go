// Package toolerrors provides structured error types for tool invocation failures.
// ToolError preserves error chains and supports errors.Is/As while maintaining
// serialization compatibility for agent-as-tool scenarios.
package toolerrors

import (
	"errors"
	"fmt"
)

// ToolError represents a structured tool failure that preserves message and causal
// context while still implementing the standard error interface. Tool errors may be
// nested via Cause to retain rich diagnostics across retries and agent-as-tool hops.
type ToolError struct {
	// Message is the human-readable summary of the failure.
	Message string
	// Kind is a stable machine-readable classification for runtime policy.
	Kind Kind `json:"kind,omitempty"`
	// Cause links to the underlying tool error, enabling error chains with errors.Is/As.
	Cause *ToolError
}

// Kind classifies a tool error for runtime policy and durable workflow records.
type Kind string

const (
	// KindOutcomeUnknown means execution may have caused an effect, but its
	// terminal result could not be confirmed.
	KindOutcomeUnknown Kind = "outcome_unknown"
)

// New constructs a ToolError with the provided message. Use when the failure does not
// wrap an underlying error but still requires structured reporting.
func New(message string) *ToolError {
	if message == "" {
		message = "tool error"
	}
	return &ToolError{Message: message}
}

// NewWithCause constructs a ToolError that wraps an underlying error. The cause is
// converted into a ToolError chain so error metadata survives serialization while still
// supporting errors.Is/As through Unwrap.
func NewWithCause(message string, cause error) *ToolError {
	if message == "" {
		if cause != nil {
			message = cause.Error()
		} else {
			message = "tool error"
		}
	}
	toolCause := FromError(cause)
	var kind Kind
	if toolCause != nil {
		kind = toolCause.Kind
	}
	return &ToolError{
		Message: message,
		Kind:    kind,
		Cause:   toolCause,
	}
}

// NewWithKind constructs a ToolError with a stable machine-readable kind.
func NewWithKind(message string, kind Kind) *ToolError {
	toolErr := New(message)
	toolErr.Kind = kind
	return toolErr
}

// FromError converts an arbitrary error into a ToolError chain.
func FromError(err error) *ToolError {
	if err == nil {
		return nil
	}
	// A direct assertion is intentional: errors.As would skip wrapper messages and
	// discard the diagnostic context this conversion is required to preserve.
	if te, ok := err.(*ToolError); ok { //nolint:errorlint
		return te
	}
	toolCause := FromError(errors.Unwrap(err))
	var kind Kind
	if toolCause != nil {
		kind = toolCause.Kind
	}
	return &ToolError{Message: err.Error(), Kind: kind, Cause: toolCause}
}

// Errorf formats according to a format specifier and returns the string as a ToolError.
func Errorf(format string, args ...any) *ToolError {
	return New(fmt.Sprintf(format, args...))
}

// Error implements the error interface.
func (e *ToolError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Unwrap returns the underlying tool error to support errors.Is/As. It returns nil
// for nil receivers and leaf errors.
func (e *ToolError) Unwrap() error {
	if e == nil || e.Cause == nil {
		return nil
	}
	return e.Cause
}
