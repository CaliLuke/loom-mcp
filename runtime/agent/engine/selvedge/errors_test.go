package selvedge

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/internal/cancellation"
	sv "github.com/CaliLuke/selvedge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cyclicError struct{}

func (e *cyclicError) Error() string {
	return "cycle"
}

func (e *cyclicError) Unwrap() error {
	return e
}

func TestErrorsToEngine(t *testing.T) {
	t.Run("application and retry exhaustion", func(t *testing.T) {
		root := sv.NewNonRetryableApplicationError("broken", "example", map[string]any{"key": "value"}, errors.New("cause"))
		source := &sv.RetryExhaustedError{ActivityName: "test", Attempts: 1, LastErr: root}
		got := normalizeError(source)
		var app *engine.ApplicationError
		require.ErrorAs(t, got, &app)
		assert.Equal(t, "example", app.Type)
		assert.Equal(t, "broken", app.Message)
		assert.True(t, app.NonRetryable)
		assert.Equal(t, root.Details, app.Details)
		var exhausted *sv.RetryExhaustedError
		require.ErrorAs(t, got, &exhausted)
		assert.Equal(t, source.Error(), got.Error())
	})
	t.Run("provider schema", func(t *testing.T) {
		got := normalizeError(sv.NewApplicationError("provider", "loom_mcp.provider_error", map[string]any{}, nil))
		var app *engine.ApplicationError
		require.ErrorAs(t, got, &app)
		assert.IsType(t, hooks.ProviderErrorDetails{}, app.Details)
	})
	t.Run("deadline", func(t *testing.T) {
		var timeout *engine.TimeoutError
		got := normalizeError(fmt.Errorf("activity: %w", context.DeadlineExceeded))
		require.ErrorAs(t, got, &timeout)
		assert.ErrorIs(t, got, context.DeadlineExceeded)
	})
	for name, source := range map[string]error{"native": context.Canceled, "workflow": &sv.WorkflowCancelledError{InstanceID: "id"}, "terminal": sv.NewTerminalError(context.Canceled)} {
		t.Run(name, func(t *testing.T) {
			got := normalizeError(source)
			assert.True(t, engine.IsCanceled(got))
			assert.True(t, cancellation.Only(got))
			assert.Equal(t, source.Error(), got.Error())
		})
	}
	t.Run("mixed recorded marker", func(t *testing.T) {
		got := normalizeError(sv.NewNonRetryableApplicationError("mixed", "loom_mcp.mixed_cancellation_failure", nil, errors.New("lost cancellation leaf")))
		assert.True(t, engine.IsCanceled(got))
		assert.False(t, cancellation.Only(got))
	})
	t.Run("invalid unchanged", func(t *testing.T) {
		source := &cyclicError{}
		assert.Same(t, source, normalizeError(source))
	})
}

func TestActivityErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source error
		kind   string
	}{
		{"root", engine.NewNonRetryableApplicationError("stop", "stop", map[string]any{"value": "v"}, nil), "stop"},
		{"mixed", errors.Join(context.Canceled, errors.New("broken")), "loom_mcp.mixed_cancellation_failure"},
		{"invalid", &cyclicError{}, "loom_mcp.invalid_error_graph"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := activityError(tc.source)
			//nolint:errorlint // Guard exact root type before traversing a possibly cyclic red-test input.
			_, ok := got.(*sv.ApplicationError)
			require.True(t, ok, "expected converted application root")
			var app *sv.ApplicationError
			require.ErrorAs(t, got, &app)
			assert.Equal(t, tc.kind, app.Type)
			assert.True(t, app.NonRetryable)
		})
	}
	got := activityError(fmt.Errorf("cancel: %w", context.Canceled))
	var terminal *sv.TerminalError
	require.ErrorAs(t, got, &terminal)
	assert.ErrorIs(t, got, context.Canceled)
}

func TestWorkflowErrorConversion(t *testing.T) {
	source := engine.NewNonRetryableApplicationError("failed", "kind", map[string]any{"key": "value"}, nil)
	var app *sv.ApplicationError
	require.ErrorAs(t, workflowError(source), &app)
	assert.Equal(t, source.Type, app.Type)
	assert.Equal(t, source.Details, app.Details)
	assert.True(t, app.NonRetryable)
	wrapped := fmt.Errorf("reason: %w", source)
	assert.Same(t, wrapped, workflowError(wrapped))
	protocol := fmt.Errorf("unsupported history: %w", api.ErrWorkflowProtocolMismatch)
	got := workflowError(protocol)
	require.ErrorAs(t, got, &app)
	assert.Equal(t, "loom_mcp.workflow_protocol_mismatch", app.Type)
	assert.Equal(t, protocol.Error(), app.Message)
	assert.ErrorIs(t, got, api.ErrWorkflowProtocolMismatch)
}
