package temporal

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	temporalmocks "go.temporal.io/sdk/mocks"
	temporalsdk "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/internal/cancellation"
)

type cyclicTemporalError struct {
	next error
}

type panickingTemporalError struct{}

type safeTemporalWrapper struct {
	child error
}

type panickingAsTemporalError struct{}

type statefulTemporalError struct {
	unwrapCalls int
}

func (*cyclicTemporalError) Error() string {
	return "cyclic temporal error"
}

func (e *cyclicTemporalError) Unwrap() error {
	return e.next
}

func (*panickingTemporalError) Error() string {
	panic("broken error text")
}

func (*safeTemporalWrapper) Error() string {
	return "safe temporal wrapper"
}

func (e *safeTemporalWrapper) Unwrap() error {
	return e.child
}

func (*panickingAsTemporalError) Error() string {
	return "panicking As temporal error"
}

func (*panickingAsTemporalError) As(any) bool {
	panic("broken As method")
}

func (*statefulTemporalError) Error() string {
	return "stateful temporal error"
}

func (e *statefulTemporalError) Unwrap() error {
	e.unwrapCalls++
	if e.unwrapCalls > 1 {
		panic("unwrap called more than once")
	}
	return context.Canceled
}

func TestClassifyTemporalCancellation(t *testing.T) {
	t.Parallel()

	cleanup := errors.New("cleanup failed")
	tests := []struct {
		name         string
		err          error
		wantCanceled bool
		wantSame     bool
		wantLeaves   []error
	}{
		{name: "nil"},
		{name: "cancellation", err: context.Canceled, wantCanceled: true},
		{name: "joined cancellations", err: errors.Join(context.Canceled, fmt.Errorf("wrapped: %w", context.Canceled)), wantCanceled: true},
		{name: "mixed cancellation and failure", err: errors.Join(context.Canceled, cleanup), wantLeaves: []error{context.Canceled, cleanup}},
		{name: "deadline", err: context.DeadlineExceeded, wantSame: true, wantLeaves: []error{context.DeadlineExceeded}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := classifyTemporalCancellation(test.err)
			if test.err == nil {
				require.NoError(t, got)
				return
			}
			require.Equal(t, test.wantCanceled, temporalsdk.IsCanceledError(got))
			if test.wantSame {
				require.Equal(t, test.err, got)
			}
			for _, leaf := range test.wantLeaves {
				require.ErrorIs(t, got, leaf)
			}
		})
	}
}

func TestMixedCancellationSurvivesTemporalFailureConversion(t *testing.T) {
	t.Parallel()

	cleanup := errors.New("cleanup failed")
	classified := classifyTemporalCancellation(errors.Join(context.Canceled, cleanup))
	var applicationErr *temporalsdk.ApplicationError
	require.ErrorAs(t, classified, &applicationErr)
	require.True(t, applicationErr.NonRetryable())
	require.Equal(t, mixedCancellationFailureType, applicationErr.Type())

	converter := temporalsdk.GetDefaultFailureConverter()
	roundTripped := converter.FailureToError(converter.ErrorToFailure(classified))
	normalized := normalizeTemporalError(roundTripped)
	require.True(t, cancellation.Contains(normalized))
	require.False(t, cancellation.Only(normalized))
	require.ErrorIs(t, normalized, context.Canceled)
	require.ErrorContains(t, normalized, cleanup.Error())
}

func TestInvalidErrorGraphUsesSafeTemporalEnvelope(t *testing.T) {
	t.Parallel()

	cyclic := &cyclicTemporalError{}
	cyclic.next = cyclic
	classified := classifyTemporalCancellation(cyclic)
	var applicationErr *temporalsdk.ApplicationError
	require.ErrorAs(t, classified, &applicationErr)
	require.True(t, applicationErr.NonRetryable())
	require.Equal(t, invalidErrorGraphFailureType, applicationErr.Type())
	require.NotPanics(t, func() {
		converter := temporalsdk.GetDefaultFailureConverter()
		roundTripped := converter.FailureToError(converter.ErrorToFailure(classified))
		require.Error(t, roundTripped)
	})
	require.NotPanics(t, func() {
		panicEnvelope := classifyTemporalCancellation(&panickingTemporalError{})
		var panicApplicationErr *temporalsdk.ApplicationError
		require.ErrorAs(t, panicEnvelope, &panicApplicationErr)
		require.Equal(t, invalidErrorGraphFailureType, panicApplicationErr.Type())
	})
	require.NotPanics(t, func() {
		nested := &safeTemporalWrapper{child: errors.Join(context.Canceled, &panickingTemporalError{})}
		panicEnvelope := classifyTemporalCancellation(nested)
		var panicApplicationErr *temporalsdk.ApplicationError
		require.ErrorAs(t, panicEnvelope, &panicApplicationErr)
		require.Equal(t, invalidErrorGraphFailureType, panicApplicationErr.Type())
		converter := temporalsdk.GetDefaultFailureConverter()
		require.Error(t, converter.FailureToError(converter.ErrorToFailure(panicEnvelope)))
	})
}

func TestTemporalCancellationClassificationUsesOneTraversal(t *testing.T) {
	t.Parallel()

	stateful := &statefulTemporalError{}
	classified := classifyTemporalCancellation(stateful)
	require.True(t, temporalsdk.IsCanceledError(classified))
	require.Equal(t, 1, stateful.unwrapCalls)
}

func TestMixedCancellationActivityDoesNotRetry(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	activity := func(context.Context) error {
		attempts.Add(1)
		return classifyTemporalCancellation(errors.Join(context.Canceled, errors.New("cleanup failed")))
	}
	workflowFn := func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
			RetryPolicy: &temporalsdk.RetryPolicy{
				MaximumAttempts: 3,
			},
		})
		return workflow.ExecuteActivity(ctx, activity).Get(ctx, nil)
	}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivity(activity)
	env.ExecuteWorkflow(workflowFn)
	require.Error(t, env.GetWorkflowError())
	require.EqualValues(t, 1, attempts.Load())
	normalized := normalizeTemporalError(env.GetWorkflowError())
	require.True(t, cancellation.Contains(normalized))
	require.False(t, cancellation.Only(normalized))
}

func TestTemporalChildHandleRestoresMixedCancellationEvidence(t *testing.T) {
	t.Parallel()

	type probe struct {
		ContainsCancellation bool
		CancellationOnly     bool
		MatchesContext       bool
	}
	childWorkflow := func(workflow.Context, *api.RunInput) error {
		return classifyTemporalCancellation(errors.Join(context.Canceled, errors.New("cleanup failed")))
	}
	parentWorkflow := func(ctx workflow.Context) (probe, error) {
		wfCtx := newTemporalWorkflowContext(workflowContextTestEngine(), ctx)
		child, err := wfCtx.StartChildWorkflow(context.Background(), engine.ChildWorkflowRequest{
			ID:       "mixed-child",
			Workflow: "mixedChildWorkflow",
		})
		if err != nil {
			return probe{}, err
		}
		_, err = child.Get(context.Background())
		return probe{
			ContainsCancellation: cancellation.Contains(err),
			CancellationOnly:     cancellation.Only(err),
			MatchesContext:       errors.Is(err, context.Canceled),
		}, nil
	}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(childWorkflow, workflow.RegisterOptions{Name: "mixedChildWorkflow"})
	env.ExecuteWorkflow(parentWorkflow)
	require.NoError(t, env.GetWorkflowError())
	var got probe
	require.NoError(t, env.GetWorkflowResult(&got))
	require.True(t, got.ContainsCancellation)
	require.False(t, got.CancellationOnly)
	require.True(t, got.MatchesContext)
}

func TestNormalizeTemporalErrorClassifiesCompleteGraph(t *testing.T) {
	t.Parallel()

	cleanup := errors.New("cleanup failed")
	require.ErrorIs(t, normalizeTemporalError(temporalsdk.NewCanceledError("canceled")), context.Canceled)
	require.ErrorIs(t, normalizeTemporalError(errors.Join(context.Canceled, temporalsdk.NewCanceledError("canceled"))), context.Canceled)
	mixed := errors.Join(temporalsdk.NewCanceledError("canceled"), cleanup)
	got := normalizeTemporalError(mixed)
	require.ErrorIs(t, got, cleanup)
	require.NotErrorIs(t, got, context.Canceled)
	require.ErrorIs(t, normalizeTemporalError(context.DeadlineExceeded), context.DeadlineExceeded)
}

func TestNormalizeTemporalErrorBoundsHostileGraphs(t *testing.T) {
	t.Parallel()

	cyclic := &cyclicTemporalError{}
	cyclic.next = cyclic
	result := make(chan error, 1)
	go func() {
		result <- normalizeTemporalError(cyclic)
	}()
	select {
	case got := <-result:
		require.Same(t, cyclic, got)
	case <-time.After(time.Second):
		t.Fatal("normalizeTemporalError hung on a cyclic graph")
	}

	panickingAs := &panickingAsTemporalError{}
	require.NotPanics(t, func() {
		require.Same(t, panickingAs, normalizeTemporalError(panickingAs))
	})
}

func TestTopLevelTemporalCompletionRestoresMixedCancellationEvidence(t *testing.T) {
	t.Parallel()

	workflowFn := func(workflow.Context) error {
		return classifyTemporalCancellation(errors.Join(context.Canceled, errors.New("cleanup failed")))
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(workflowFn)
	workflowErr := env.GetWorkflowError()
	require.Error(t, workflowErr)

	ctx := context.Background()
	handleRun := temporalmocks.NewWorkflowRun(t)
	handleRun.On("Get", ctx, mock.Anything).Return(workflowErr).Once()
	handle := &workflowHandle{run: handleRun}
	_, handleErr := handle.Wait(ctx)
	require.ErrorIs(t, handleErr, context.Canceled)
	require.True(t, cancellation.Contains(handleErr))
	require.False(t, cancellation.Only(handleErr))
	require.ErrorContains(t, handleErr, "cleanup failed")

	queryRun := temporalmocks.NewWorkflowRun(t)
	queryRun.On("Get", ctx, mock.Anything).Return(workflowErr).Once()
	client := temporalmocks.NewClient(t)
	client.On("GetWorkflow", ctx, "mixed-workflow", "").Return(queryRun).Once()
	engine := &Engine{client: client}
	_, queryErr := engine.QueryRunCompletion(ctx, "mixed-workflow")
	require.ErrorIs(t, queryErr, context.Canceled)
	require.True(t, cancellation.Contains(queryErr))
	require.False(t, cancellation.Only(queryErr))
	require.ErrorContains(t, queryErr, "cleanup failed")
}
