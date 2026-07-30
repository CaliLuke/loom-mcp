package temporal

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"
	workflowservice "go.temporal.io/api/workflowservice/v1"
	temporalmocks "go.temporal.io/sdk/mocks"
)

func TestWorkflowHandleDelegatesWaitSignalAndCancel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := temporalmocks.NewClient(t)
	run := temporalmocks.NewWorkflowRun(t)
	run.On("GetID").Return("workflow-1")
	run.On("GetRunID").Return("run-1")
	run.On("Get", ctx, mock.Anything).Run(func(args mock.Arguments) {
		out := args.Get(1).(**api.RunOutput)
		*out = &api.RunOutput{RunID: "run-1"}
	}).Return(nil).Once()
	client.On("SignalWorkflow", ctx, "workflow-1", "run-1", "pause", "payload").Return(nil).Once()
	client.On("CancelWorkflow", ctx, "workflow-1", "run-1").Return(serviceerror.NewFailedPrecondition("completed")).Once()
	handle := &workflowHandle{run: run, client: client}

	out, err := handle.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, "run-1", out.RunID)
	require.NoError(t, handle.Signal(ctx, "pause", "payload"))
	require.ErrorIs(t, handle.Cancel(ctx), engine.ErrWorkflowCompleted)
}

func TestWorkflowHandleWaitAndSignalPropagateErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	wantErr := errors.New("temporal unavailable")
	client := temporalmocks.NewClient(t)
	run := temporalmocks.NewWorkflowRun(t)
	run.On("Get", ctx, mock.Anything).Return(wantErr).Once()
	run.On("GetID").Return("workflow-1")
	run.On("GetRunID").Return("run-1")
	client.On("SignalWorkflow", ctx, "workflow-1", "run-1", "pause", mock.Anything).Return(serviceerror.NewNotFound("missing")).Once()
	handle := &workflowHandle{run: run, client: client}

	_, err := handle.Wait(ctx)
	require.ErrorIs(t, err, wantErr)
	require.ErrorIs(t, handle.Signal(ctx, "pause", nil), engine.ErrWorkflowNotFound)
}

func TestEngineDirectWorkflowOperations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := temporalmocks.NewClient(t)
	eng := &Engine{client: client}
	encoded := temporalmocks.NewEncodedValue(t)
	client.On("SignalWorkflow", ctx, "workflow-1", "run-1", "resume", "payload").Return(nil).Once()
	client.On("CancelWorkflow", ctx, "workflow-1", "").Return(serviceerror.NewNotFound("missing")).Once()
	client.On("QueryWorkflow", ctx, "workflow-1", "", "state", "arg").Return(encoded, nil).Once()
	client.On("DescribeWorkflowExecution", ctx, "workflow-1", "").Return(&workflowservice.DescribeWorkflowExecutionResponse{}, nil).Once()

	require.NoError(t, eng.SignalByID(ctx, "workflow-1", "run-1", "resume", "payload"))
	require.ErrorIs(t, eng.CancelByID(ctx, "workflow-1"), engine.ErrWorkflowNotFound)
	value, err := eng.QueryWorkflow(ctx, "workflow-1", "state", "arg")
	require.NoError(t, err)
	assert.Same(t, encoded, value)
	status, err := eng.QueryRunStatus(ctx, "workflow-1")
	require.NoError(t, err)
	assert.Equal(t, engine.RunStatusPending, status)
}

func TestEngineWorkflowOperationValidation(t *testing.T) {
	t.Parallel()

	eng := &Engine{}
	ctx := context.Background()
	require.ErrorContains(t, eng.SignalByID(ctx, "", "", "pause", nil), "workflow id is required")
	require.ErrorContains(t, eng.CancelByID(ctx, ""), "workflow id is required")
	_, err := eng.QueryWorkflow(ctx, "", "state")
	require.ErrorContains(t, err, "workflow id is required")
	_, err = eng.QueryWorkflow(ctx, "workflow-1", "")
	require.ErrorContains(t, err, "query type is required")
	_, err = eng.QueryRunStatus(ctx, "")
	require.ErrorContains(t, err, "workflow id is required")
	_, err = eng.QueryRunCompletion(ctx, "")
	require.ErrorContains(t, err, "workflow id is required")
}

func TestQueryRunCompletionMapsNotFoundAndReturnsOutput(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := temporalmocks.NewClient(t)
	notFoundRun := temporalmocks.NewWorkflowRun(t)
	notFoundRun.On("Get", ctx, mock.Anything).Return(serviceerror.NewNotFound("missing")).Once()
	successRun := temporalmocks.NewWorkflowRun(t)
	successRun.On("Get", ctx, mock.Anything).Run(func(args mock.Arguments) {
		out := args.Get(1).(**api.RunOutput)
		*out = &api.RunOutput{RunID: "run-1"}
	}).Return(nil).Once()
	client.On("GetWorkflow", ctx, "missing", "").Return(notFoundRun).Once()
	client.On("GetWorkflow", ctx, "workflow-1", "").Return(successRun).Once()
	eng := &Engine{client: client}

	_, err := eng.QueryRunCompletion(ctx, "missing")
	require.ErrorIs(t, err, engine.ErrWorkflowNotFound)
	out, err := eng.QueryRunCompletion(ctx, "workflow-1")
	require.NoError(t, err)
	assert.Equal(t, "run-1", out.RunID)
}
