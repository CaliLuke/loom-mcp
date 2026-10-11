package enginetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
)

// activityBarrier announces execution from an activity, never from workflow I/O.
func activityBarrier(t *testing.T, f Fixture, block bool) (<-chan struct{}, func()) {
	t.Helper()
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	ctx := f.ctx
	require.NoError(t, f.Engine.RegisterHookActivity(ctx, activityName, activityOptions(f), func(actx context.Context, _ *api.HookActivityInput) error {
		entered <- struct{}{}
		if !block {
			return nil
		}
		select {
		case <-release:
			return nil
		case <-actx.Done():
			return actx.Err()
		}
	}))
	// Cleanup closes the channel even when an assertion aborts the scenario.
	t.Cleanup(func() {
		close(release)
	})
	return entered, func() {
		release <- struct{}{}
	}
}

func announce(w engine.WorkflowContext) error {
	return w.PublishHook(w.Context(), engine.HookActivityCall{Name: activityName, Input: &api.HookActivityInput{RunID: w.WorkflowID()}})
}

func testLifecycle(t *testing.T, f Fixture) error {
	return testWorkflowLifecycle(t, f, false)
}

func testIDConflict(t *testing.T, f Fixture) error {
	return testWorkflowLifecycle(t, f, true)
}

func testWorkflowLifecycle(t *testing.T, f Fixture, conflict bool) error {
	ctx := f.ctx
	entered, release := activityBarrier(t, f, true)
	input := request(f).Input
	registerWorkflow(t, f, workflowName, func(w engine.WorkflowContext, in *api.RunInput) (*api.RunOutput, error) {
		if err := checkEqual(input, in); err != nil {
			return nil, err
		}
		if w.WorkflowID() != input.RunID || w.RunID() == "" {
			return nil, errors.New("workflow identity not preserved")
		}
		if err := announce(w); err != nil {
			return nil, err
		}
		return success(), nil
	})
	h := start(t, f, ctx)
	receive(t, ctx, entered)
	status, err := f.Engine.QueryRunStatus(ctx, input.RunID)
	if err != nil {
		return err
	}
	if err := checkEqual(engine.RunStatusRunning, status); err != nil {
		return err
	}
	if conflict {
		_, err = f.Engine.StartWorkflow(ctx, request(f))
		if err == nil {
			return &GapError{Err: errors.New("active workflow IDs must conflict")}
		}
		if errors.Is(err, engine.ErrWorkflowStartRejected) {
			return &GapError{Err: errors.New("ID conflict must not claim definite start rejection")}
		}
	}
	release()
	if err := waitSuccess(ctx, h); err != nil {
		return err
	}
	status, err = f.Engine.QueryRunStatus(ctx, input.RunID)
	if err != nil {
		return err
	}
	return checkEqual(engine.RunStatusCompleted, status)
}

func testQueries(t *testing.T, f Fixture) error {
	ctx := f.ctx
	entered, release := activityBarrier(t, f, true)
	registerWorkflow(t, f, workflowName, func(w engine.WorkflowContext, in *api.RunInput) (*api.RunOutput, error) {
		if err := w.SetQueryHandler(api.QueryRootRunID, func() (string, error) {
			return in.RootRunID, nil
		}); err != nil {
			return nil, err
		}
		if err := announce(w); err != nil {
			return nil, err
		}
		return success(), nil
	})
	h := start(t, f, ctx)
	receive(t, ctx, entered)
	root, err := f.Engine.QueryRootRunID(ctx, request(f).ID)
	if err != nil {
		return err
	}
	if err := checkEqual(request(f).Input.RootRunID, root); err != nil {
		return err
	}
	release()
	return waitSuccess(ctx, h)
}

func testTimerNow(t *testing.T, f Fixture) error {
	ctx := f.ctx
	registerWorkflow(t, f, workflowName, func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		before := w.Now()
		if before.IsZero() || w.Now().Before(before) {
			return nil, errors.New("Now must be nonzero and monotonic")
		}
		timer, err := w.NewTimer(w.Context(), time.Millisecond)
		if err != nil {
			return nil, err
		}
		if err := w.Await(w.Context(), timer.IsReady); err != nil {
			return nil, err
		}
		fired, err := timer.Get(w.Context())
		if err != nil {
			return nil, err
		}
		if fired.Before(before.Add(time.Millisecond)) || w.Now().Before(fired) {
			return nil, errors.New("timer/Now regressed or timer fired early")
		}
		return success(), nil
	})
	return waitSuccess(ctx, start(t, f, ctx))
}

func testCancellation(t *testing.T, f Fixture) error {
	ctx := f.ctx
	entered, _ := activityBarrier(t, f, false)
	cleaned := registerCleanup(t, f)
	registerWorkflow(t, f, workflowName, func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		err := announce(w)
		if err == nil {
			_, err = w.ResumeRequests().Receive(w.Context())
		}
		if err == nil {
			return nil, errors.New("external cancellation did not interrupt workflow wait")
		}
		if cleanupErr := cleanup(w.Detached()); cleanupErr != nil {
			return nil, cleanupErr
		}
		return nil, err
	})
	h := start(t, f, ctx)
	receive(t, ctx, entered)
	require.NoError(t, h.Cancel(ctx))
	receive(t, ctx, cleaned)
	_, err := h.Wait(ctx)
	return checkCanceled(err)
}

func testCancelScope(t *testing.T, f Fixture) error {
	ctx := f.ctx
	cleaned := registerCleanup(t, f)
	registerWorkflow(t, f, workflowName, func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		scope, cancel := w.WithCancel()
		timer, err := scope.NewTimer(scope.Context(), time.Hour)
		if err != nil {
			return nil, err
		}
		cancel()
		if _, err := timer.Get(scope.Context()); !errors.Is(err, context.Canceled) {
			return nil, errors.New("WithCancel did not cancel its scope")
		}
		if err := cleanup(w); err != nil {
			return nil, err
		}
		return success(), nil
	})
	if err := waitSuccess(ctx, start(t, f, ctx)); err != nil {
		return err
	}
	receive(t, ctx, cleaned)
	return nil
}

func registerCleanup(t *testing.T, f Fixture) <-chan struct{} {
	t.Helper()
	cleaned := make(chan struct{}, 2)
	require.NoError(t, f.Engine.RegisterExecuteToolActivity(t.Context(), cleanupName, activityOptions(f), func(ctx context.Context, in *api.ToolInput) (*api.ToolOutput, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if in.ToolCallID != cleanupName {
			return nil, errors.New("cleanup payload lost")
		}
		cleaned <- struct{}{}
		return &api.ToolOutput{Preview: "cleaned"}, nil
	}))
	return cleaned
}

func cleanup(w engine.WorkflowContext) error {
	out, err := w.ExecuteToolActivity(w.Context(), engine.ToolActivityCall{Name: cleanupName, Input: &api.ToolInput{ToolCallID: cleanupName}})
	if err != nil {
		return err
	}
	return checkEqual(&api.ToolOutput{Preview: "cleaned"}, out)
}

func testAwaitSignal(t *testing.T, f Fixture) error {
	ctx := f.ctx
	entered, _ := activityBarrier(t, f, false)
	payload := &api.ResumeRequest{RunID: request(f).ID, Notes: "continue"}
	registerWorkflow(t, f, workflowName, func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		return awaitSignalWorkflow(w, payload)
	})
	h := start(t, f, ctx)
	receive(t, ctx, entered)
	require.Eventually(t, func() bool {
		root, err := f.Engine.QueryRootRunID(ctx, request(f).ID)
		return err == nil && root == rootRunID
	}, 5*time.Second, time.Millisecond, "workflow must reach the empty signal wait")
	require.NoError(t, h.Signal(ctx, api.SignalResume, payload))
	return waitSuccess(ctx, h)
}

// awaitSignalWorkflow blocks in Await on Pending, then proves that inspection
// left the signal available for ReceiveAsync.
func awaitSignalWorkflow(w engine.WorkflowContext, payload *api.ResumeRequest) (*api.RunOutput, error) {
	receiver := w.ResumeRequests()
	if err := checkEqual(0, receiver.Pending()); err != nil {
		return nil, err
	}
	if err := announce(w); err != nil {
		return nil, err
	}
	// Queries observe the wait only after the announcement activity completes.
	if err := w.SetQueryHandler(api.QueryRootRunID, func() (string, error) {
		return rootRunID, checkEqual(0, receiver.Pending())
	}); err != nil {
		return nil, err
	}
	if err := w.Await(w.Context(), func() bool { return receiver.Pending() > 0 }); err != nil {
		return nil, err
	}
	return receivePendingSignal(receiver, payload)
}

// receivePendingSignal checks that repeated Pending calls do not consume the
// signal and that ReceiveAsync then returns it.
func receivePendingSignal(receiver engine.Receiver[*api.ResumeRequest], payload *api.ResumeRequest) (*api.RunOutput, error) {
	for range 2 {
		if err := checkEqual(1, receiver.Pending()); err != nil {
			return nil, err
		}
	}
	got, ok := receiver.ReceiveAsync()
	if !ok {
		return nil, errors.New("Await consumed the pending signal")
	}
	if err := checkEqual(payload, got); err != nil {
		return nil, err
	}
	return success(), checkEqual(0, receiver.Pending())
}
