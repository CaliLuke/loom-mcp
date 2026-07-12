package inmem

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActivityRegistrationValidation(t *testing.T) {
	t.Parallel()

	e := New().(*eng)
	ctx := context.Background()
	hook := func(context.Context, *api.HookActivityInput) error { return nil }
	planner := func(context.Context, *api.PlanActivityInput) (*api.PlanActivityOutput, error) {
		return &api.PlanActivityOutput{}, nil
	}
	tool := func(context.Context, *api.ToolInput) (*api.ToolOutput, error) { return &api.ToolOutput{}, nil }

	require.ErrorContains(t, e.RegisterWorkflow(ctx, engine.WorkflowDefinition{}), "invalid workflow definition")
	require.NoError(t, e.RegisterWorkflow(ctx, engine.WorkflowDefinition{Name: "workflow", Handler: func(engine.WorkflowContext, *api.RunInput) (*api.RunOutput, error) { return nil, nil }}))
	require.ErrorContains(t, e.RegisterWorkflow(ctx, engine.WorkflowDefinition{Name: "workflow", Handler: func(engine.WorkflowContext, *api.RunInput) (*api.RunOutput, error) { return nil, nil }}), "already registered")

	require.ErrorContains(t, e.RegisterHookActivity(ctx, "", engine.ActivityOptions{}, hook), "name is required")
	require.ErrorContains(t, e.RegisterHookActivity(ctx, "hook", engine.ActivityOptions{}, nil), "handler is required")
	require.NoError(t, e.RegisterHookActivity(ctx, "hook", engine.ActivityOptions{}, hook))
	require.ErrorContains(t, e.RegisterHookActivity(ctx, "hook", engine.ActivityOptions{}, hook), "already registered")

	require.ErrorContains(t, e.RegisterPlannerActivity(ctx, "", engine.ActivityOptions{}, planner), "name is required")
	require.ErrorContains(t, e.RegisterPlannerActivity(ctx, "planner", engine.ActivityOptions{}, nil), "handler is required")
	require.NoError(t, e.RegisterPlannerActivity(ctx, "planner", engine.ActivityOptions{}, planner))
	require.ErrorContains(t, e.RegisterPlannerActivity(ctx, "planner", engine.ActivityOptions{}, planner), "already registered")

	require.ErrorContains(t, e.RegisterExecuteToolActivity(ctx, "", engine.ActivityOptions{}, tool), "name is required")
	require.ErrorContains(t, e.RegisterExecuteToolActivity(ctx, "tool", engine.ActivityOptions{}, nil), "handler is required")
	require.NoError(t, e.RegisterExecuteToolActivity(ctx, "tool", engine.ActivityOptions{}, tool))
	require.ErrorContains(t, e.RegisterExecuteToolActivity(ctx, "tool", engine.ActivityOptions{}, tool), "already registered")
}

func TestWorkflowContextActivityExecutionAndTimeouts(t *testing.T) {
	t.Parallel()

	e := New().(*eng)
	w := e.newWorkflowContext(context.Background(), "run-1")
	hookInput := &api.HookActivityInput{}
	require.NoError(t, e.RegisterHookActivity(context.Background(), "hook", engine.ActivityOptions{}, func(_ context.Context, got *api.HookActivityInput) error {
		assert.Same(t, hookInput, got)
		return nil
	}))
	require.NoError(t, w.PublishHook(context.Background(), engine.HookActivityCall{Name: "hook", Input: hookInput}))

	require.NoError(t, e.RegisterHookActivity(context.Background(), "slow-hook", engine.ActivityOptions{}, func(ctx context.Context, _ *api.HookActivityInput) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	err := w.PublishHook(context.Background(), engine.HookActivityCall{
		Name:    "slow-hook",
		Input:   hookInput,
		Options: engine.ActivityOptions{StartToCloseTimeout: time.Millisecond},
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)

	require.ErrorContains(t, w.PublishHook(context.Background(), engine.HookActivityCall{}), "name is required")
	require.ErrorContains(t, w.PublishHook(context.Background(), engine.HookActivityCall{Name: "hook"}), "input is required")
	require.ErrorContains(t, w.PublishHook(context.Background(), engine.HookActivityCall{Name: "missing", Input: hookInput}), "not registered")

	require.ErrorContains(t, func() error {
		_, callErr := w.ExecutePlannerActivity(context.Background(), engine.PlannerActivityCall{})
		return callErr
	}(), "name is required")
	require.ErrorContains(t, func() error {
		_, callErr := w.ExecutePlannerActivity(context.Background(), engine.PlannerActivityCall{Name: "missing", Input: &api.PlanActivityInput{}})
		return callErr
	}(), "not registered")

	require.ErrorContains(t, func() error {
		_, callErr := w.ExecuteToolActivity(context.Background(), engine.ToolActivityCall{})
		return callErr
	}(), "name is required")
	require.ErrorContains(t, func() error {
		_, callErr := w.ExecuteToolActivity(context.Background(), engine.ToolActivityCall{Name: "missing", Input: &api.ToolInput{}})
		return callErr
	}(), "not registered")
}

func TestWorkflowContextSignals(t *testing.T) {
	t.Parallel()

	e := New().(*eng)
	w := e.newWorkflowContext(context.Background(), "run-1")
	h := &handle{done: make(chan struct{}), wfCtx: w, cancel: func() {}}

	pause := &api.PauseRequest{}
	require.NoError(t, h.Signal(context.Background(), api.SignalPause, pause))
	gotPause, ok := w.PauseRequests().ReceiveAsync()
	require.True(t, ok)
	assert.Same(t, pause, gotPause)

	resume := &api.ResumeRequest{}
	require.NoError(t, h.Signal(context.Background(), api.SignalResume, resume))
	gotResume, err := w.ResumeRequests().ReceiveWithTimeout(context.Background(), time.Second)
	require.NoError(t, err)
	assert.Same(t, resume, gotResume)

	clarification := &api.ClarificationAnswer{}
	require.NoError(t, h.Signal(context.Background(), api.SignalProvideClarification, clarification))
	gotClarification, err := w.ClarificationAnswers().Receive(context.Background())
	require.NoError(t, err)
	assert.Same(t, clarification, gotClarification)

	results := &api.ToolResultsSet{}
	require.NoError(t, h.Signal(context.Background(), api.SignalProvideToolResults, results))
	gotResults, ok := w.ExternalToolResults().ReceiveAsync()
	require.True(t, ok)
	assert.Same(t, results, gotResults)

	confirmation := &api.ConfirmationDecision{}
	require.NoError(t, h.Signal(context.Background(), api.SignalProvideConfirmation, confirmation))
	gotConfirmation, ok := w.ConfirmationDecisions().ReceiveAsync()
	require.True(t, ok)
	assert.Same(t, confirmation, gotConfirmation)

	typed := &api.TypedInputAnswer{}
	require.NoError(t, h.Signal(context.Background(), api.SignalProvideTypedInput, typed))
	gotTyped, ok := w.TypedInputAnswers().ReceiveAsync()
	require.True(t, ok)
	assert.Same(t, typed, gotTyped)

	for _, name := range []string{
		api.SignalPause,
		api.SignalResume,
		api.SignalProvideClarification,
		api.SignalProvideToolResults,
		api.SignalProvideConfirmation,
		api.SignalProvideTypedInput,
	} {
		require.ErrorContains(t, h.Signal(context.Background(), name, "wrong"), "expects")
	}
	require.ErrorContains(t, h.Signal(context.Background(), "unknown", nil), "unknown signal")

	_, ok = w.PauseRequests().ReceiveAsync()
	assert.False(t, ok)
	_, err = w.PauseRequests().ReceiveWithTimeout(context.Background(), 0)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = w.PauseRequests().Receive(canceled)
	require.ErrorIs(t, err, context.Canceled)

	close(h.done)
	require.ErrorIs(t, h.Signal(context.Background(), api.SignalPause, pause), engine.ErrWorkflowCompleted)
}

func TestWorkflowContextTimeAndCancellation(t *testing.T) {
	t.Parallel()

	parent, cancelParent := context.WithCancel(context.Background())
	e := New().(*eng)
	w := e.newWorkflowContext(parent, "run-1")
	require.NoError(t, w.SetQueryHandler("status", func() string { return "running" }))
	assert.Equal(t, "run-1", w.WorkflowID())
	assert.Equal(t, "run-1", w.RunID())
	assert.False(t, w.Now().IsZero())

	detached := w.Detached()
	cancelParent()
	require.ErrorIs(t, w.Context().Err(), context.Canceled)
	require.NoError(t, detached.Context().Err())

	child, cancelChild := detached.WithCancel()
	require.NoError(t, child.Context().Err())
	cancelChild()
	require.ErrorIs(t, child.Context().Err(), context.Canceled)

	immediate, err := detached.NewTimer(context.Background(), 0)
	require.NoError(t, err)
	assert.True(t, immediate.IsReady())
	firedAt, err := immediate.Get(context.Background())
	require.NoError(t, err)
	assert.False(t, firedAt.IsZero())

	timer, err := detached.NewTimer(context.Background(), time.Millisecond)
	require.NoError(t, err)
	assert.False(t, timer.IsReady())
	_, err = timer.Get(context.Background())
	require.NoError(t, err)
	assert.True(t, timer.IsReady())

	require.ErrorContains(t, detached.Await(context.Background(), nil), "condition is required")
	require.NoError(t, detached.Await(context.Background(), func() bool { return true }))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, detached.Await(canceled, func() bool { return false }), context.Canceled)
}
