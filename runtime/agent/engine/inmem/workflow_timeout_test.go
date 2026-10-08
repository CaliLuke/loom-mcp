package inmem

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowRunTimeoutFailsRun(t *testing.T) {
	eng := New()
	require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{
		Name: "timeout",
		Handler: func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
			<-w.Context().Done()
			return nil, w.Context().Err()
		},
	}))
	h, err := eng.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "timed", Workflow: "timeout", RunTimeout: 20 * time.Millisecond})
	require.NoError(t, err)
	t.Cleanup(func() {
		err := h.Cancel(context.Background())
		if !errors.Is(err, engine.ErrWorkflowCompleted) {
			require.NoError(t, err)
		}
	})
	guard, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err = h.Wait(guard)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	status, err := eng.QueryRunStatus(t.Context(), "timed")
	require.NoError(t, err)
	assert.Equal(t, engine.RunStatusFailed, status)
	assert.NoError(t, guard.Err(), "workflow timeout must precede the test watchdog")
}

func TestWorkflowRunTimeoutContextAndCleanup(t *testing.T) {
	for _, tt := range []struct {
		name         string
		timeout      time.Duration
		wantDeadline bool
	}{
		{name: "zero"},
		{name: "positive", timeout: time.Hour, wantDeadline: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			eng := New()
			observed := make(chan context.Context, 1)
			require.NoError(t, eng.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{
				Name: "complete",
				Handler: func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
					observed <- w.Context()
					return &api.RunOutput{RunID: "success"}, nil
				},
			}))
			h, err := eng.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: tt.name, Workflow: "complete", RunTimeout: tt.timeout})
			require.NoError(t, err)
			guard, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			out, err := h.Wait(guard)
			require.NoError(t, err)
			assert.Equal(t, "success", out.RunID)
			ctx := <-observed
			_, hasDeadline := ctx.Deadline()
			assert.Equal(t, tt.wantDeadline, hasDeadline)
			if tt.wantDeadline {
				assert.ErrorIs(t, ctx.Err(), context.Canceled, "completed run must release its deadline")
			}
		})
	}
}
