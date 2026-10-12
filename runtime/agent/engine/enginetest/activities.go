package enginetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
)

type activityCase struct {
	name          string
	input, output any
	register      func(context.Context, engine.Engine, engine.ActivityOptions, func(context.Context, any) (any, error)) error
	invoke        func(engine.WorkflowContext, any, engine.ActivityOptions) (any, error)
}

func activityScenarios() []scenario {
	cases := make([]scenario, 0, 6*len(activities()))
	for _, a := range activities() {
		cases = append(cases,
			scenario{name: "activities/" + a.name, run: func(t *testing.T, f Fixture) error {
				return testActivity(t, f, a, false)
			}},
			scenario{name: "application_errors/" + a.name, run: func(t *testing.T, f Fixture) error {
				return testApplicationError(t, f, a)
			}},
			scenario{name: "activity_errors/" + a.name, run: func(t *testing.T, f Fixture) error {
				return testActivity(t, f, a, true)
			}},
			scenario{name: "activity_options/" + a.name, run: func(t *testing.T, f Fixture) error {
				return testActivityOptions(t, f, a)
			}},
			scenario{name: "activity_attempt/" + a.name, run: func(t *testing.T, f Fixture) error {
				return testAttempt(t, f, a, false)
			}},
			scenario{name: "activity_retry/" + a.name, capability: "Retries", run: func(t *testing.T, f Fixture) error {
				return testAttempt(t, f, a, true)
			}},
		)
	}
	return cases
}

func testActivity(t *testing.T, f Fixture, a activityCase, fail bool) error {
	ctx := f.ctx
	opts := activityOptions(f)
	require.NoError(t, a.register(ctx, f.Engine, opts, func(_ context.Context, input any) (any, error) {
		if err := checkEqual(a.input, input); err != nil {
			return nil, err
		}
		if fail {
			return nil, errors.New(applicationFailure)
		}
		return a.output, nil
	}))
	registerWorkflow(t, f, workflowName, func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		got, err := a.invoke(w, a.input, engine.ActivityOptions{})
		if err != nil {
			return nil, err
		}
		return success(), checkEqual(a.output, got)
	})
	h := start(t, f, ctx)
	if fail {
		_, err := h.Wait(ctx)
		return checkApplicationFailure(err)
	}
	return waitSuccess(ctx, h)
}

func testActivityOptions(t *testing.T, f Fixture, a activityCase) error {
	ctx := f.ctx
	defaults := activityOptions(f)
	override := defaults
	override.StartToCloseTimeout = 8 * time.Second
	override.ScheduleToStartTimeout = 9 * time.Second
	override.HeartbeatTimeout = 4 * time.Second
	override.RetryPolicy = engine.RetryPolicy{MaxAttempts: 2, InitialInterval: 2 * time.Millisecond, BackoffCoefficient: 2}
	partial := engine.ActivityOptions{RetryPolicy: engine.RetryPolicy{MaxAttempts: 3}}
	merged := defaults
	merged.RetryPolicy.MaxAttempts = 3
	want := []engine.ActivityOptions{defaults, override, merged}
	heartbeats := make(chan time.Duration, len(want))
	require.NoError(t, a.register(ctx, f.Engine, defaults, func(actx context.Context, input any) (any, error) {
		heartbeats <- engine.ActivityHeartbeatTimeout(actx)
		return a.output, checkEqual(a.input, input)
	}))
	registerWorkflow(t, f, workflowName, func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		for _, opts := range []engine.ActivityOptions{{}, override, partial} {
			got, err := a.invoke(w, a.input, opts)
			if err != nil {
				return nil, err
			}
			if err := checkEqual(a.output, got); err != nil {
				return nil, err
			}
		}
		return success(), nil
	})
	if err := waitSuccess(ctx, start(t, f, ctx)); err != nil {
		return err
	}
	for _, opts := range want {
		observed := receive(t, ctx, heartbeats)
		if observed != opts.HeartbeatTimeout {
			return &GapError{Err: fmt.Errorf("activity heartbeat option: want %s, got %s", opts.HeartbeatTimeout, observed)}
		}
	}
	// Missing inspection is a fixture failure, never an expected semantic failure.
	require.NotNil(t, f.VerifyActivityOptions, "factory must observe actual backend options")
	return f.VerifyActivityOptions(t, request(f).ID, activityName, want)
}

func testAttempt(t *testing.T, f Fixture, a activityCase, retry bool) error {
	ctx := f.ctx
	attempts := make(chan int, 2)
	opts := activityOptions(f)
	if retry {
		opts.RetryPolicy.MaxAttempts = 2
	}
	require.NoError(t, a.register(ctx, f.Engine, opts, func(actx context.Context, _ any) (any, error) {
		attempt := engine.ActivityAttemptFromContext(actx)
		attempts <- attempt
		if retry && attempt == 1 {
			return nil, errors.New(applicationFailure)
		}
		return a.output, nil
	}))
	registerWorkflow(t, f, workflowName, func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		got, err := a.invoke(w, a.input, engine.ActivityOptions{})
		if err != nil {
			return nil, err
		}
		return success(), checkEqual(a.output, got)
	})
	if err := waitSuccess(ctx, start(t, f, ctx)); err != nil {
		return err
	}
	if err := checkEqual(1, receive(t, ctx, attempts)); err != nil {
		return err
	}
	if retry {
		if err := checkEqual(2, receive(t, ctx, attempts)); err != nil {
			return err
		}
	}
	select {
	case extra := <-attempts:
		return fmt.Errorf("unexpected extra attempt %d", extra)
	default:
	}
	return nil
}

func testParallelTools(t *testing.T, f Fixture) error {
	return testParallelToolBatch(t, f, false)
}

func testParallelToolFailure(t *testing.T, f Fixture) error {
	return testParallelToolBatch(t, f, true)
}

func testParallelToolBatch(t *testing.T, f Fixture, fail bool) error {
	ctx := f.ctx
	entered := make(chan string, 3)
	release := make(chan struct{})
	t.Cleanup(func() {
		close(release)
	})
	require.NoError(t, f.Engine.RegisterExecuteToolActivity(ctx, activityName, activityOptions(f), func(actx context.Context, in *api.ToolInput) (*api.ToolOutput, error) {
		entered <- in.ToolCallID
		select {
		case <-release:
			if fail && in.ToolCallID == "b" {
				return nil, errors.New(applicationFailure)
			}
			return &api.ToolOutput{Preview: in.ToolCallID, Payload: in.Payload}, nil
		case <-actx.Done():
			return nil, actx.Err()
		}
	}))
	registerWorkflow(t, f, workflowName, parallelWorkflow)
	h := start(t, f, ctx)
	ids := []string{receive(t, ctx, entered), receive(t, ctx, entered), receive(t, ctx, entered)}
	slices.Sort(ids)
	if err := checkEqual([]string{"a", "b", "c"}, ids); err != nil {
		return err
	}
	// Release all activities only after every future has independently started.
	for range 3 {
		release <- struct{}{}
	}
	if fail {
		_, err := h.Wait(ctx)
		return checkApplicationFailure(err)
	}
	return waitSuccess(ctx, h)
}

func parallelWorkflow(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
	futures := make([]engine.Future[*api.ToolOutput], 0, 3)
	for _, id := range []string{"a", "b", "c"} {
		fut, err := w.ExecuteToolActivityAsync(w.Context(), engine.ToolActivityCall{Name: activityName, Input: &api.ToolInput{ToolCallID: id, Payload: rawjson.Message(`{"nested":[true,"value"]}`)}})
		if err != nil {
			return nil, err
		}
		futures = append(futures, fut)
	}
	pending := []bool{true, true, true}
	var activityErr error
	for remaining := 3; remaining > 0; {
		if err := w.Await(w.Context(), func() bool {
			for i, fut := range futures {
				if pending[i] && fut.IsReady() {
					return true
				}
			}
			return false
		}); err != nil {
			return nil, err
		}
		count, err := collectReadyTools(w, futures, pending)
		if err != nil && activityErr == nil {
			activityErr = err
		}
		remaining -= count
	}
	return success(), activityErr
}

// collectReadyTools drains only ready futures and checks repeatable Get results.
func collectReadyTools(w engine.WorkflowContext, futures []engine.Future[*api.ToolOutput], pending []bool) (int, error) {
	count := 0
	var firstErr error
	for i, fut := range futures {
		if !pending[i] || !fut.IsReady() {
			continue
		}
		err := checkToolFuture(w, fut, []string{"a", "b", "c"}[i])
		if err != nil && firstErr == nil {
			firstErr = err
		}
		pending[i] = false
		count++
	}
	return count, firstErr
}

// checkToolFuture checks both result/error propagation and repeatable Get.
func checkToolFuture(w engine.WorkflowContext, fut engine.Future[*api.ToolOutput], id string) error {
	got, err := fut.Get(w.Context())
	again, repeatedErr := fut.Get(w.Context())
	if err != nil {
		if repeatedErr == nil || repeatedErr.Error() != err.Error() {
			return errors.New("future did not preserve its activity error")
		}
		return err
	}
	if repeatedErr != nil {
		return repeatedErr
	}
	if err := checkEqual(got, again); err != nil {
		return err
	}
	want := &api.ToolOutput{Preview: id, Payload: rawjson.Message(`{"nested":[true,"value"]}`)}
	return checkEqual(want, got)
}

// typedResult preserves errors without requiring a non-nil activity output.
func typedResult[T any](out any, err error) (T, error) {
	if err != nil {
		var zero T
		return zero, err
	}
	v, ok := out.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("invalid test activity result %T", out)
	}
	return v, nil
}
