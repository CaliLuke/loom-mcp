package temporal

import (
	"errors"
	"reflect"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/internal/cancellation"
	"go.temporal.io/sdk/temporal"
)

type temporalErrorNodes struct {
	originalApplication *temporal.ApplicationError
}

// applicationError is an adapter-owned wrapper whose display text is the exact
// backend text. Its neutral node carries portable metadata and the SDK cause.
type applicationError struct {
	application *engine.ApplicationError
	backend     error
}

// canceledError retains the original diagnostic cause while exposing only the
// engine cancellation leaf to bounded classification. As and Is preserve SDK
// inspection without reintroducing an SDK leaf into the runtime error graph.
type canceledError struct {
	Cause error
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

func (e *canceledError) As(target any) bool {
	return errors.As(e.Cause, target)
}

func (e *canceledError) Is(target error) bool {
	return errors.Is(e.Cause, target)
}

// normalizeTemporalError exposes engine types and keeps the original SDK error
// as their cause. The bounded traversal also restores mixed-cancellation evidence.
func normalizeTemporalError(err error) error {
	if err == nil {
		return nil
	}
	var failures []error
	mixed := false
	inspection := cancellation.InspectWithCancellationLeaf(err, func(candidate error, _ string) bool {
		//nolint:errorlint // Inspect supplies exact validated graph nodes.
		switch typed := candidate.(type) {
		case *temporal.ApplicationError:
			failures = append(failures, typed)
			mixed = mixed || typed.Type() == mixedCancellationFailureType
		case *temporal.TimeoutError:
			failures = append(failures, typed)
		}
		return false
	}, isTemporalCancellationLeaf)
	if !inspection.Valid {
		return err
	}
	if inspection.OnlyCancellation {
		return &canceledError{Cause: err}
	}
	converted := err
	for i := len(failures) - 1; i >= 0; i-- {
		//nolint:errorlint // These are exact SDK nodes from the bounded inspection.
		switch failure := failures[i].(type) {
		case *temporal.ApplicationError:
			details, decodeErr := applicationDetails(failure)
			if decodeErr != nil {
				return errors.Join(err, decodeErr)
			}
			converted = &applicationError{application: &engine.ApplicationError{Type: failure.Type(), Message: failure.Message(), NonRetryable: failure.NonRetryable(), Details: details, Cause: converted}, backend: err}
		case *temporal.TimeoutError:
			converted = &engine.TimeoutError{Cause: converted}
		}
	}
	if mixed || inspection.ContainsCancellation {
		return errors.Join(engine.ErrCanceled, converted)
	}
	return converted
}

// applicationDetails decodes the provider schema explicitly; other application
// details use the data converter's generic single-value representation.
func applicationDetails(application *temporal.ApplicationError) (any, error) {
	if !application.HasDetails() {
		return nil, nil
	}
	if application.Type() == "loom_mcp.provider_error" {
		var envelope hooks.ProviderErrorDetails
		if err := application.Details(&envelope); err != nil {
			return nil, err
		}
		return envelope, nil
	}
	var details any
	if err := application.Details(&details); err != nil {
		return nil, err
	}
	return details, nil
}

// temporalApplicationError preserves the former SDK construction exactly. When
// workflow code re-returns a converted failure unchanged, restore its original
// SDK chain rather than adding another serialized application failure layer.
func temporalApplicationError(application *engine.ApplicationError, original *temporal.ApplicationError) error {
	if original != nil && original.Type() == application.Type && original.Message() == application.Message && original.NonRetryable() == application.NonRetryable {
		details, err := applicationDetails(original)
		if err == nil && reflect.DeepEqual(details, application.Details) {
			return backendCause(application.Cause)
		}
	}
	opts := temporal.ApplicationErrorOptions{NonRetryable: application.NonRetryable, Cause: application.Cause}
	if application.Details != nil {
		opts.Details = []any{application.Details}
	}
	return temporal.NewApplicationErrorWithOptions(application.Message, application.Type, opts)
}

func isTemporalCancellationLeaf(err error) bool {
	//nolint:errorlint // Called only for an exact leaf by the bounded classifier.
	_, ok := err.(*temporal.CanceledError)
	return ok
}

// convertRootTemporalError converts only a root engine node or an adapter-owned
// application wrapper. External joins and reason wrappers retain their identity,
// text, top-level failure type and retryability.
func convertRootTemporalError(err error, original *temporal.ApplicationError) error {
	//nolint:errorlint // Only the exact root may be converted; nested nodes cannot be hoisted.
	switch root := err.(type) {
	case *applicationError:
		return temporalApplicationError(root.application, original)
	case *engine.ApplicationError:
		return temporalApplicationError(root, original)
	case *engine.TimeoutError:
		return backendCause(root.Cause)
	default:
		return err
	}
}

// backendCause restores only the adapter's own synthetic application wrapper.
// Other cause graphs, including caller-authored engine nodes, remain intact.
func backendCause(err error) error {
	//nolint:errorlint // This exact wrapper was created by normalization.
	if synthetic, ok := err.(*applicationError); ok {
		return synthetic.backend
	}
	return err
}

// match records exact graph nodes during the adapter's one bounded inspection.
func (n *temporalErrorNodes) match(candidate error, _ string) bool {
	//nolint:errorlint // Inspect supplies each exact graph node.
	if typed, ok := candidate.(*temporal.ApplicationError); ok && n.originalApplication == nil {
		n.originalApplication = typed
	}
	return false
}
