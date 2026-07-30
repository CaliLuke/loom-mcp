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

func TestWorkflowLifecycleStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		handlerErr error
		wantStatus engine.RunStatus
	}{
		{name: "completed", wantStatus: engine.RunStatusCompleted},
		{name: "failed", handlerErr: errors.New("failed"), wantStatus: engine.RunStatusFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			eng := New()
			require.NoError(t, eng.RegisterWorkflow(context.Background(), engine.WorkflowDefinition{
				Name: "workflow",
				Handler: func(engine.WorkflowContext, *api.RunInput) (*api.RunOutput, error) {
					return &api.RunOutput{}, tt.handlerErr
				},
			}))

			h, err := eng.StartWorkflow(context.Background(), engine.WorkflowStartRequest{ID: tt.name, Workflow: "workflow", Input: &api.RunInput{}})
			require.NoError(t, err)
			_, err = h.Wait(context.Background())
			if tt.handlerErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.handlerErr)
			}
			status, err := eng.QueryRunStatus(context.Background(), tt.name)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, status)
		})
	}
}

func TestStartWorkflowRejectsDuplicateRunID(t *testing.T) {
	t.Parallel()

	eng := New()
	release := make(chan struct{})
	require.NoError(t, eng.RegisterWorkflow(context.Background(), engine.WorkflowDefinition{
		Name: "workflow",
		Handler: func(engine.WorkflowContext, *api.RunInput) (*api.RunOutput, error) {
			<-release
			return &api.RunOutput{}, nil
		},
	}))

	first, err := eng.StartWorkflow(context.Background(), engine.WorkflowStartRequest{ID: "same-run", Workflow: "workflow", Input: &api.RunInput{}})
	require.NoError(t, err)
	_, err = eng.StartWorkflow(context.Background(), engine.WorkflowStartRequest{ID: "same-run", Workflow: "workflow", Input: &api.RunInput{}})
	require.ErrorContains(t, err, "already exists")

	close(release)
	_, err = first.Wait(context.Background())
	require.NoError(t, err)
}

func TestWorkflowHandleCancelStopsRun(t *testing.T) {
	t.Parallel()

	eng := New()
	started := make(chan struct{})
	require.NoError(t, eng.RegisterWorkflow(context.Background(), cancelableWorkflow(started)))
	h, err := eng.StartWorkflow(context.Background(), engine.WorkflowStartRequest{ID: "cancel-handle", Workflow: "cancelable", Input: &api.RunInput{}})
	require.NoError(t, err)
	<-started

	require.NoError(t, h.Cancel(context.Background()))
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = h.Wait(waitCtx)
	require.ErrorIs(t, err, context.Canceled)
	status, err := eng.QueryRunStatus(context.Background(), "cancel-handle")
	require.NoError(t, err)
	assert.Equal(t, engine.RunStatusCanceled, status)
	require.ErrorIs(t, h.Cancel(context.Background()), engine.ErrWorkflowCompleted)
}

func TestCancelByIDStopsRunAndRejectsUnknownID(t *testing.T) {
	t.Parallel()

	eng := New()
	canceler := eng.(engine.Canceler)
	started := make(chan struct{})
	require.NoError(t, eng.RegisterWorkflow(context.Background(), cancelableWorkflow(started)))
	h, err := eng.StartWorkflow(context.Background(), engine.WorkflowStartRequest{ID: "cancel-by-id", Workflow: "cancelable", Input: &api.RunInput{}})
	require.NoError(t, err)
	<-started

	require.NoError(t, canceler.CancelByID(context.Background(), "cancel-by-id"))
	_, err = h.Wait(context.Background())
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, canceler.CancelByID(context.Background(), "missing"), engine.ErrWorkflowNotFound)
}

func TestChildWorkflowReportsRunID(t *testing.T) {
	t.Parallel()

	eng := New()
	require.NoError(t, eng.RegisterWorkflow(context.Background(), engine.WorkflowDefinition{
		Name: "child",
		Handler: func(engine.WorkflowContext, *api.RunInput) (*api.RunOutput, error) {
			return &api.RunOutput{}, nil
		},
	}))
	require.NoError(t, eng.RegisterWorkflow(context.Background(), engine.WorkflowDefinition{
		Name: "parent",
		Handler: func(ctx engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
			child, err := ctx.StartChildWorkflow(ctx.Context(), engine.ChildWorkflowRequest{ID: "child-run", Workflow: "child", Input: &api.RunInput{}})
			if err != nil {
				return nil, err
			}
			if child.RunID() != "child-run" {
				return nil, errors.New("child run ID was not preserved")
			}
			return child.Get(ctx.Context())
		},
	}))

	h, err := eng.StartWorkflow(context.Background(), engine.WorkflowStartRequest{ID: "parent-run", Workflow: "parent", Input: &api.RunInput{}})
	require.NoError(t, err)
	_, err = h.Wait(context.Background())
	require.NoError(t, err)
}

func TestQueryRunStatusValidation(t *testing.T) {
	t.Parallel()

	eng := New()
	_, err := eng.QueryRunStatus(context.Background(), "")
	require.ErrorContains(t, err, "workflow id is required")
	_, err = eng.QueryRunStatus(context.Background(), "missing")
	require.ErrorIs(t, err, engine.ErrWorkflowNotFound)
}

func cancelableWorkflow(started chan<- struct{}) engine.WorkflowDefinition {
	return engine.WorkflowDefinition{
		Name: "cancelable",
		Handler: func(ctx engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
			close(started)
			<-ctx.Context().Done()
			return nil, ctx.Context().Err()
		},
	}
}
