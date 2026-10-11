package inmem

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/internal/cancellation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeError(t *testing.T) {
	application := engine.NewNonRetryableApplicationError("invalid", "example.invalid", map[string]any{"code": "bad"}, nil)
	for _, test := range []struct {
		name              string
		err               error
		canceled, timeout bool
	}{
		{name: "nil"},
		{name: "application", err: application},
		{name: "cancel", err: context.Canceled, canceled: true},
		{name: "deadline", err: context.DeadlineExceeded, timeout: true},
		{name: "mixed", err: errors.Join(context.Canceled, errors.New("cleanup failed")), canceled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeError(test.err)
			assert.Equal(t, test.canceled, engine.IsCanceled(got))
			assert.Equal(t, test.timeout, engine.IsTimeout(got))
			if test.timeout {
				var timeout *engine.TimeoutError
				require.ErrorAs(t, got, &timeout)
				require.ErrorIs(t, timeout, test.err)
			} else {
				assert.Equal(t, test.err, got)
			}
		})
	}
	assert.False(t, cancellation.Only(normalizeError(errors.Join(context.Canceled, errors.New("cleanup failed")))))
}

func TestTimerCancellationMarker(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w := &wfCtx{ctx: ctx}
	timer, err := w.NewTimer(ctx, time.Hour)
	require.NoError(t, err)
	_, err = timer.Get(ctx)
	require.ErrorIs(t, err, engine.ErrCanceled)
}
