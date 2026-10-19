package control

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io/fs"

	"github.com/CaliLuke/loom-mcp/v2/eval/candidate"
	"github.com/CaliLuke/loom-mcp/v2/eval/inspect"
)

// Code identifies a safe control failure class.
type Code string

// Error exposes approved diagnostics while retaining a Go-only private cause.
type Error struct {
	Code     Code   `json:"code"`
	Message  string `json:"message"`
	Recovery string `json:"recovery"`
	Field    string `json:"field,omitempty"`
	cause    error
}

const (
	// InvalidInput means a request must be repaired.
	InvalidInput Code = "invalid_input"
	// RevisionConflict means the draft revision changed.
	RevisionConflict Code = "revision_conflict"
	// FrozenConflict means immutable content cannot be edited.
	FrozenConflict Code = "frozen"
	// NotFound hides unknown and unauthorized identities.
	NotFound Code = "not_found"
	// Unsupported means the host has not granted this operation.
	Unsupported Code = "unsupported"
	// RequestConflict means an idempotency key was reused with different input.
	RequestConflict Code = "request_conflict"
	// ExecutorUnavailable means execution cannot be accepted.
	ExecutorUnavailable Code = "executor_unavailable"
	// EvidenceNotReady means the job has no committed evidence association yet.
	EvidenceNotReady Code = "evidence_not_ready"
	// OperationFailed means an operational failure needs host attention.
	OperationFailed Code = "operation_failed"
)

// NewError returns approved static text for code and a safe allowlisted field.
// Supplied message and recovery are deliberately not trusted as public text.
func NewError(code Code, _, _, field string) *Error {
	e := &Error{Code: code, Field: safeField(field)}
	switch code {
	case InvalidInput:
		e.Message = "Invalid request or candidate content."
		e.Recovery = "Repair the indicated field or component and validate the draft."
	case RevisionConflict:
		e.Message = "Draft revision changed."
		e.Recovery = "Get the current draft revision and retry the edit."
		if e.Field == jsonPlanDigest {
			e.Message = "Copy confirmation does not match the current preview."
			e.Recovery = "Preview the current destination and confirm the complete returned plan before copying."
		}
	case FrozenConflict:
		e.Message = "Candidate is frozen."
		e.Recovery = "Create a new draft from the frozen candidate."
	case NotFound:
		e.Message = "Requested resource was not found."
		e.Recovery = "Discover authorized resources and use their returned handles."
	case Unsupported:
		e.Message = "Operation is not supported by this host."
		e.Recovery = "Use the operations returned by discovery."
	case RequestConflict:
		e.Message = "Request key is bound to different input."
		e.Recovery = "Retry the original input with this key, or use a new key for a new trial."
	case ExecutorUnavailable:
		e.Message = "Host executor is unavailable."
		e.Recovery = "Ask the host to restore execution, then retry the same request key."
	case EvidenceNotReady:
		e.Message = "Job evidence is not available yet."
		e.Recovery = "Poll the job and retry after evidence is available."
	case OperationFailed:
		e.Message = "Control operation failed."
		e.Recovery = "Ask the host to inspect diagnostics; retry submissions with the same request key."
	default:
		e.Code = OperationFailed
		e.Message = "Control operation failed."
		e.Recovery = "Ask the host to inspect diagnostics; retry submissions with the same request key."
	}
	return e
}

// Error serializes only approved public diagnostics, including for host-created errors.
func (e *Error) Error() string {
	safe := NewError(e.Code, "", "", e.Field)
	data, err := json.Marshal(safe)
	if err != nil {
		return `{"code":"operation_failed","message":"Control operation failed.","recovery":"Ask the host to inspect diagnostics."}`
	}
	return string(data)
}

// Unwrap exposes the private diagnostic cause only to Go callers.
func (e *Error) Unwrap() error {
	return e.cause
}

func safeField(field string) string {
	switch field {
	case jsonComponents, jsonReplace, jsonPlanDigest, jsonBaseline, jsonSourceCandidate, jsonDraftId, jsonCandidate, jsonExpectedRevision, "component", jsonContent, jsonContentBase64, jsonDependencies, jsonSlot, jsonMediaType, jsonOperation, jsonOffset, "limit", jsonMaxBytes, "request_key", "task_id", jsonKind, jsonEvaluationPlan, jsonJobId, jsonLocator:
		return field
	default:
		return ""
	}
}

func invalid(field string) error {
	return NewError(InvalidInput, "", "", field)
}

func publicError(err error, fallback Code, field string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	code := fallback
	var ce *Error
	switch {
	case errors.As(err, &ce):
		code = ce.Code
		field = ce.Field
	case errors.Is(err, candidate.ErrCopyPlan):
		code = RevisionConflict
		field = jsonPlanDigest
	case errors.Is(err, candidate.ErrRevision):
		code = RevisionConflict
		field = jsonExpectedRevision
	case errors.Is(err, candidate.ErrFrozen):
		code = FrozenConflict
	case errors.Is(err, candidate.ErrNotFound), errors.Is(err, fs.ErrNotExist), errors.Is(err, inspect.ErrNotFound):
		code = NotFound
	case errors.Is(err, inspect.ErrInvalidInput):
		code = InvalidInput
	}
	out := NewError(code, "", "", field)
	out.cause = err
	return out
}
