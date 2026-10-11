package engine_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplicationError(t *testing.T) {
	cause := errors.New("provider failure")
	details := map[string]any{"code": "invalid"}
	for _, nonRetryable := range []bool{false, true} {
		t.Run(fmt.Sprint(nonRetryable), func(t *testing.T) {
			err := engine.NewApplicationError("request failed", "example.invalid", details, cause)
			if nonRetryable {
				err = engine.NewNonRetryableApplicationError("request failed", "example.invalid", details, cause)
			}
			assert.Equal(t, "request failed: provider failure", err.Error())
			assert.Equal(t, "example.invalid", err.Type)
			assert.Equal(t, nonRetryable, err.NonRetryable)
			assert.Equal(t, details, err.Details)
			require.ErrorIs(t, err, cause)
			var application *engine.ApplicationError
			require.ErrorAs(t, fmt.Errorf("wrapped: %w", err), &application)
			assert.Same(t, err, application)
		})
	}
}

func TestEngineErrorMarkers(t *testing.T) {
	for _, test := range []struct {
		name              string
		err               error
		canceled, timeout bool
	}{
		{name: "nil"},
		{name: "canceled", err: engine.ErrCanceled, canceled: true},
		{name: "wrapped cancellation", err: fmt.Errorf("wrapped: %w", engine.ErrCanceled), canceled: true},
		{name: "context cancellation", err: context.Canceled, canceled: true},
		{name: "timeout", err: &engine.TimeoutError{Cause: context.DeadlineExceeded}, timeout: true},
		{name: "deadline", err: context.DeadlineExceeded, timeout: true},
		{name: "failure", err: errors.New("failure")},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.canceled, engine.IsCanceled(test.err))
			assert.Equal(t, test.timeout, engine.IsTimeout(test.err))
		})
	}
}
