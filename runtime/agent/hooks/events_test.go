package hooks

import (
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/internal/cancellation"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

type cyclicHookError struct {
	next error
}

type panickingHookUnwrapError struct{}

type panickingHookTextError struct{}

func (*cyclicHookError) Error() string {
	return "cyclic hook error"
}

func (e *cyclicHookError) Unwrap() error {
	return e.next
}

func (*panickingHookUnwrapError) Error() string {
	return "panicking hook unwrap"
}

func (*panickingHookUnwrapError) Unwrap() error {
	panic("broken hook unwrap")
}

func (*panickingHookTextError) Error() string {
	panic("broken hook text")
}

func TestNewRunCompletedEventPreservesTemporalProviderErrorEnvelope(t *testing.T) {
	providerErr := model.NewProviderError(
		"bedrock",
		"converse_stream",
		429,
		model.ProviderErrorKindRateLimited,
		"ThrottlingException",
		"too many requests",
		"req-1",
		true,
		errors.New("throttled"),
	)

	err := WrapRunCompletionError(providerErr)
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	require.False(t, appErr.NonRetryable())

	evt := NewRunCompletedEvent("run-1", "svc.agent", "sess-1", "failed", run.PhaseFailed, err)

	require.Equal(t, PublicErrorProviderRateLimited, evt.PublicError)
	require.Equal(t, "bedrock", evt.ErrorProvider)
	require.Equal(t, "converse_stream", evt.ErrorOperation)
	require.Equal(t, string(model.ProviderErrorKindRateLimited), evt.ErrorKind)
	require.Equal(t, "ThrottlingException", evt.ErrorCode)
	require.Equal(t, 429, evt.HTTPStatus)
	require.True(t, evt.Retryable)
}

func TestCompletionHooksSanitizeInvalidErrorGraphs(t *testing.T) {
	t.Parallel()

	cyclic := &cyclicHookError{}
	cyclic.next = cyclic
	wrapped := make(chan error, 1)
	go func() {
		wrapped <- WrapRunCompletionError(cyclic)
	}()
	select {
	case err := <-wrapped:
		require.ErrorIs(t, err, cancellation.ErrInvalidErrorGraph)
	case <-time.After(time.Second):
		t.Fatal("WrapRunCompletionError hung on a cyclic graph")
	}

	events := make(chan *RunCompletedEvent, 1)
	go func() {
		events <- NewRunCompletedEvent("run-1", "svc.agent", "sess-1", string(run.StatusFailed), run.PhaseFailed, cyclic)
	}()
	select {
	case event := <-events:
		require.ErrorIs(t, event.Error, cancellation.ErrInvalidErrorGraph)
		require.Equal(t, ErrorKindInternal, event.ErrorKind)
		require.Equal(t, PublicErrorInternal, event.PublicError)
	case <-time.After(time.Second):
		t.Fatal("NewRunCompletedEvent hung on a cyclic graph")
	}

	for _, hostile := range []error{&panickingHookUnwrapError{}, &panickingHookTextError{}} {
		require.NotPanics(t, func() {
			require.ErrorIs(t, WrapRunCompletionError(hostile), cancellation.ErrInvalidErrorGraph)
			event := NewRunCompletedEvent("run-1", "svc.agent", "sess-1", string(run.StatusFailed), run.PhaseFailed, hostile)
			require.ErrorIs(t, event.Error, cancellation.ErrInvalidErrorGraph)
		})
	}
}
