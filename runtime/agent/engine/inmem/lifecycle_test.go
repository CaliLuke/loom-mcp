package inmem

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowLifecycleStatuses(t *testing.T) {
	t.Parallel()

	ordinary := errors.New("failed")
	cleanup := errors.New("cleanup failed")
	cancellationOnly := errors.Join(context.Canceled, fmt.Errorf("wrapped: %w", context.Canceled))
	mixed := errors.Join(context.Canceled, cleanup)
	tests := []struct {
		name       string
		handlerErr error
		wantStatus engine.RunStatus
		wantLeaves []error
	}{
		{name: "completed", wantStatus: engine.RunStatusCompleted},
		{name: "failed", handlerErr: ordinary, wantStatus: engine.RunStatusFailed, wantLeaves: []error{ordinary}},
		{name: "cancellation only", handlerErr: cancellationOnly, wantStatus: engine.RunStatusCanceled, wantLeaves: []error{context.Canceled}},
		{name: "mixed cancellation and failure", handlerErr: mixed, wantStatus: engine.RunStatusFailed, wantLeaves: []error{context.Canceled, cleanup}},
		{name: "deadline", handlerErr: context.DeadlineExceeded, wantStatus: engine.RunStatusFailed, wantLeaves: []error{context.DeadlineExceeded}},
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
			if len(tt.wantLeaves) == 0 {
				require.NoError(t, err)
			} else {
				for _, leaf := range tt.wantLeaves {
					require.ErrorIs(t, err, leaf)
				}
			}
			status, err := eng.QueryRunStatus(context.Background(), tt.name)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, status)
		})
	}
}

func TestWorkflowCancellationPreservesHandlerCleanupFailure(t *testing.T) {
	t.Parallel()

	eng := New()
	started := make(chan struct{})
	cleanup := errors.New("cleanup failed")
	require.NoError(t, eng.RegisterWorkflow(context.Background(), engine.WorkflowDefinition{
		Name: "mixed-cancellation",
		Handler: func(ctx engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
			close(started)
			<-ctx.Context().Done()
			return nil, cleanup
		},
	}))
	handle, err := eng.StartWorkflow(context.Background(), engine.WorkflowStartRequest{ID: "mixed-cancellation", Workflow: "mixed-cancellation", Input: &api.RunInput{}})
	require.NoError(t, err)
	<-started
	require.NoError(t, handle.Cancel(context.Background()))
	_, err = handle.Wait(context.Background())
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, cleanup)
	status, err := eng.QueryRunStatus(context.Background(), "mixed-cancellation")
	require.NoError(t, err)
	require.Equal(t, engine.RunStatusFailed, status)
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

func TestAcceptedWorkflowOutlivesStartRequestContext(t *testing.T) {
	t.Parallel()

	type contextKey struct{}
	eng := New()
	started := make(chan struct{})
	retainedValue := make(chan string, 1)
	require.NoError(t, eng.RegisterWorkflow(context.Background(), engine.WorkflowDefinition{
		Name: "wait-for-signal",
		Handler: func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
			close(started)
			_, err := w.PauseRequests().Receive(context.Background())
			if err != nil {
				return nil, err
			}
			retainedValue <- w.Context().Value(contextKey{}).(string)
			return &api.RunOutput{RunID: "completed"}, nil
		},
	}))

	startCtx, cancelStart := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "request-value"))
	h, err := eng.StartWorkflow(startCtx, engine.WorkflowStartRequest{ID: "accepted-run", Workflow: "wait-for-signal"})
	require.NoError(t, err)
	<-started
	cancelStart()
	require.NoError(t, h.Signal(context.Background(), api.SignalPause, &api.PauseRequest{}))
	require.Equal(t, "request-value", <-retainedValue)

	out, err := h.Wait(context.Background())
	require.NoError(t, err)
	require.Equal(t, "completed", out.RunID)
}

func TestStartWorkflowRejectsCanceledRequestBeforeAcceptance(t *testing.T) {
	t.Parallel()

	eng := New()
	started := make(chan struct{}, 1)
	require.NoError(t, eng.RegisterWorkflow(context.Background(), engine.WorkflowDefinition{
		Name: "workflow",
		Handler: func(engine.WorkflowContext, *api.RunInput) (*api.RunOutput, error) {
			started <- struct{}{}
			return &api.RunOutput{RunID: "accepted"}, nil
		},
	}))

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	h, err := eng.StartWorkflow(requestCtx, engine.WorkflowStartRequest{ID: "reusable-run", Workflow: "workflow"})
	require.Nil(t, h)
	require.ErrorIs(t, err, context.Canceled)
	_, err = eng.QueryRunStatus(context.Background(), "reusable-run")
	require.ErrorIs(t, err, engine.ErrWorkflowNotFound)
	select {
	case <-started:
		t.Fatal("canceled request started the workflow handler")
	default:
	}

	h, err = eng.StartWorkflow(context.Background(), engine.WorkflowStartRequest{ID: "reusable-run", Workflow: "workflow"})
	require.NoError(t, err)
	out, err := h.Wait(context.Background())
	require.NoError(t, err)
	require.Equal(t, "accepted", out.RunID)
	select {
	case <-started:
	default:
		t.Fatal("accepted workflow did not invoke its handler")
	}
}

func TestParentCancellationCancelsChildWorkflow(t *testing.T) {
	t.Parallel()

	eng := New()
	childStarted := make(chan struct{})
	childHandleCh := make(chan engine.ChildWorkflowHandle, 1)
	require.NoError(t, eng.RegisterWorkflow(context.Background(), engine.WorkflowDefinition{
		Name: "child",
		Handler: func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
			close(childStarted)
			<-w.Context().Done()
			return nil, w.Context().Err()
		},
	}))
	require.NoError(t, eng.RegisterWorkflow(context.Background(), engine.WorkflowDefinition{
		Name: "parent",
		Handler: func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
			child, err := w.StartChildWorkflow(w.Context(), engine.ChildWorkflowRequest{
				ID: "child-run", Workflow: "child", Input: &api.RunInput{},
			})
			if err != nil {
				return nil, err
			}
			childHandleCh <- child
			<-w.Context().Done()
			return nil, w.Context().Err()
		},
	}))

	parent, err := eng.StartWorkflow(context.Background(), engine.WorkflowStartRequest{ID: "parent-run", Workflow: "parent"})
	require.NoError(t, err)
	child := <-childHandleCh
	<-childStarted
	require.NoError(t, parent.Cancel(context.Background()))
	_, parentErr := parent.Wait(context.Background())
	require.ErrorIs(t, parentErr, context.Canceled)
	_, childErr := child.Get(context.Background())
	require.ErrorIs(t, childErr, context.Canceled)
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
