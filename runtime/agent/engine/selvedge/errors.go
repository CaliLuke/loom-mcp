package selvedge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/internal/cancellation"
	sv "github.com/CaliLuke/selvedge"
)

type errorNodes struct {
	applications             []*sv.ApplicationError
	deadline, timeout, mixed bool
}

type applicationError struct {
	application *engine.ApplicationError
	backend     error
}

type canceledError struct {
	Cause error
}

const mixedCancellationFailureType = "loom_mcp.mixed_cancellation_failure"

func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	var nodes errorNodes
	inspection := cancellation.InspectWithCancellationLeaf(err, nodes.match, nil)
	if !inspection.Valid {
		return err
	}
	if inspection.OnlyCancellation {
		return &canceledError{Cause: err}
	}
	converted := err
	if nodes.deadline && !nodes.timeout {
		converted = &engine.TimeoutError{Cause: converted}
	}
	for i := len(nodes.applications) - 1; i >= 0; i-- {
		source := nodes.applications[i]
		details, decodeErr := applicationDetails(source)
		if decodeErr != nil {
			return errors.Join(err, decodeErr)
		}
		converted = &applicationError{application: &engine.ApplicationError{Type: source.Type, Message: source.Message, NonRetryable: source.NonRetryable, Details: details, Cause: converted}, backend: err}
	}
	if nodes.mixed || inspection.ContainsCancellation {
		return errors.Join(engine.ErrCanceled, converted)
	}
	return converted
}

func applicationDetails(source *sv.ApplicationError) (any, error) {
	if source.Details == nil {
		return nil, nil
	}
	data, err := json.Marshal(source.Details)
	if err != nil {
		return nil, err
	}
	if source.Type == "loom_mcp.provider_error" {
		var details hooks.ProviderErrorDetails
		if err := json.Unmarshal(data, &details); err != nil {
			return nil, err
		}
		return details, nil
	}
	var details any
	if err := json.Unmarshal(data, &details); err != nil {
		return nil, err
	}
	return details, nil
}

func rootError(err error) error {
	var application *engine.ApplicationError
	//nolint:errorlint // Only exact root nodes and adapter-owned wrappers convert.
	switch root := err.(type) {
	case *applicationError:
		application = root.application
	case *engine.ApplicationError:
		application = root
	default:
		return err
	}
	return &sv.ApplicationError{Type: application.Type, Message: application.Message, NonRetryable: application.NonRetryable, Details: application.Details, Cause: application.Cause}
}

func activityError(err error) error {
	if err == nil {
		return nil
	}
	inspection := cancellation.Inspect(err, nil)
	if !inspection.Valid {
		return sv.NewNonRetryableApplicationError("operation failed with invalid error graph", "loom_mcp.invalid_error_graph", nil, nil)
	}
	if inspection.OnlyCancellation {
		return sv.NewTerminalError(err)
	}
	if inspection.ContainsCancellation {
		return sv.NewNonRetryableApplicationError("operation failed with mixed cancellation and failure", mixedCancellationFailureType, nil, err)
	}
	return rootError(err)
}

func workflowError(err error) error {
	if err == nil {
		return nil
	}
	inspection := cancellation.Inspect(err, func(candidate error, _ string) bool {
		return cancellation.Exact(candidate, api.ErrWorkflowProtocolMismatch)
	})
	if inspection.Valid && inspection.Matched {
		return sv.NewNonRetryableApplicationError(err.Error(), "loom_mcp.workflow_protocol_mismatch", nil, err)
	}
	return rootError(err)
}

func byIDError(err error) error {
	if errors.Is(err, sv.ErrInstanceNotFound) {
		return fmt.Errorf("%w: %w", engine.ErrWorkflowNotFound, err)
	}
	var closed *sv.InstanceClosedError
	if errors.As(err, &closed) {
		return fmt.Errorf("%w: %w", engine.ErrWorkflowCompleted, err)
	}
	return err
}

func startError(err error) error {
	if errors.Is(err, sv.ErrWorkflowAlreadyStarted) {
		return fmt.Errorf("%w: %w", engine.ErrWorkflowAlreadyStarted, err)
	}
	if errors.Is(err, sv.ErrWorkflowStartRejected) {
		return fmt.Errorf("%w: %w", engine.ErrWorkflowStartRejected, err)
	}
	return err
}

func (n *errorNodes) match(candidate error, _ string) bool {
	//nolint:errorlint // Inspect supplies exact validated graph nodes.
	switch typed := candidate.(type) {
	case *sv.ApplicationError:
		n.applications = append(n.applications, typed)
		n.mixed = n.mixed || typed.Type == mixedCancellationFailureType
	case *engine.TimeoutError:
		n.timeout = true
	}
	n.deadline = n.deadline || cancellation.Exact(candidate, context.DeadlineExceeded)
	return false
}
func (e *applicationError) Error() string {
	return e.backend.Error()
}

func (e *applicationError) Unwrap() error {
	return e.application
}

func (e *canceledError) Error() string {
	return e.Cause.Error()
}

func (e *canceledError) Unwrap() error {
	return engine.ErrCanceled
}

func (e *canceledError) Is(target error) bool {
	return errors.Is(e.Cause, target)
}

func (e *canceledError) As(target any) bool {
	return errors.As(e.Cause, target)
}
