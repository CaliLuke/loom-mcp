package runtime

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelCallBudgetSwallowedRefusal(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "stream"}[stream], func(t *testing.T) {
			rt, provider, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
				client, ok := agent.ModelClient("registered-model")
				require.True(t, ok)
				for range 3 {
					err := budgetTestInvoke(ctx, client, stream)
					if err != nil {
						assert.ErrorIs(t, err, policy.ErrModelCallLimit)
					}
				}
				return nil // A caught refusal cannot become a successful planner result.
			})
			input.MaxModelCalls = 2
			out, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), input)
			require.NoError(t, err)
			require.NotNil(t, out)
			assert.Nil(t, out.Result)
			assert.Equal(t, &policy.ModelCallLimit{Limit: 2, Call: runstore.CallKey{RunID: "child-run", Seq: 3, Attempt: 1}}, out.ModelCallLimit)
			assert.Len(t, provider.calls, 2)
			assert.Len(t, out.CallKeys, 2)
			assert.Len(t, out.Facts, 2)
			calls, err := rt.RunStore.ListCalls(t.Context(), "root-run", "child-run")
			require.NoError(t, err)
			assert.Len(t, calls, 2, "refusal is not a started provider call")
		})
	}
}

func TestModelCallBudgetConcurrentReservations(t *testing.T) {
	rt, provider, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
		client, ok := agent.ModelClient("registered-model")
		require.True(t, ok)
		start := make(chan struct{})
		errs := make(chan error, 20)
		var calls sync.WaitGroup
		for i := range 20 {
			calls.Go(func() {
				<-start
				errs <- budgetTestInvoke(ctx, client, i%2 == 0)
			})
		}
		close(start)
		calls.Wait()
		close(errs)
		refused := 0
		for err := range errs {
			if errors.Is(err, policy.ErrModelCallLimit) {
				refused++
			} else {
				assert.NoError(t, err)
			}
		}
		assert.Equal(t, 17, refused)
		return nil
	})
	input.MaxModelCalls = 3
	out, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), input)
	require.NoError(t, err)
	require.NotNil(t, out.ModelCallLimit)
	assert.Equal(t, 4, out.ModelCallLimit.Call.Seq)
	assert.Len(t, provider.calls, 3)
	assert.Len(t, out.CallKeys, 3)
}

func TestModelCallBudgetActivityRetryRetainsReservations(t *testing.T) {
	lateFailure := errors.New("activity failed after provider response")
	rt, provider, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
		client, ok := agent.ModelClient("registered-model")
		require.True(t, ok)
		if err := budgetTestInvoke(ctx, client, false); err != nil {
			return err
		}
		return lateFailure
	})
	input.MaxModelCalls = 1
	_, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), input)
	require.ErrorIs(t, err, lateFailure)
	out, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 2), input)
	require.NoError(t, err)
	require.NotNil(t, out.ModelCallLimit)
	assert.Equal(t, runstore.CallKey{RunID: "child-run", Seq: 2, Attempt: 2}, out.ModelCallLimit.Call)
	assert.Empty(t, out.CallKeys)
	assert.Len(t, provider.calls, 1)
}

func budgetTestInvoke(ctx context.Context, client model.Client, stream bool) error {
	if stream {
		result, err := client.Stream(ctx, &model.Request{Model: "provider-model"})
		if err != nil {
			return err
		}
		if _, err := result.Recv(); !errors.Is(err, io.EOF) {
			return errors.Join(err, result.Finalize(err))
		}
		return result.Finalize(nil)
	}
	_, err := client.Complete(ctx, &model.Request{Model: "provider-model"})
	return err
}

func TestModelCallBudgetPreflightFailureConsumesAllowance(t *testing.T) {
	failure := errors.New("model preflight rejected")
	rt, provider, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
		client, ok := agent.ModelClient("registered-model")
		require.True(t, ok)
		require.ErrorIs(t, budgetTestInvoke(ctx, client, false), failure)
		require.ErrorIs(t, budgetTestInvoke(ctx, client, true), policy.ErrModelCallLimit)
		return nil
	})
	intercepted := 0
	rt.interceptors = []Interceptor{RuntimeInterceptorFuncs{BeforeModelFunc: func(context.Context, *BeforeModelInput) (*BeforeModelDecision, error) {
		intercepted++
		return nil, failure
	}}}
	input.MaxModelCalls = 1
	out, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), input)
	require.NoError(t, err)
	require.NotNil(t, out.ModelCallLimit)
	assert.Equal(t, 2, out.ModelCallLimit.Call.Seq)
	assert.Equal(t, 1, intercepted)
	assert.Empty(t, provider.calls)
	assert.Empty(t, out.CallKeys)
}

func TestModelCallBudgetRunIsolation(t *testing.T) {
	rt, provider, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
		client, ok := agent.ModelClient("registered-model")
		require.True(t, ok)
		return budgetTestInvoke(ctx, client, false)
	})
	input.MaxModelCalls = 1
	parent := *input
	parent.RunID, parent.RunContext.RunID = "parent-run", "parent-run"
	for _, in := range []*PlanActivityInput{&parent, input} {
		out, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), in)
		require.NoError(t, err)
		assert.Nil(t, out.ModelCallLimit)
		require.Len(t, out.CallKeys, 1)
		assert.Equal(t, 1, out.CallKeys[0].Seq)
		assert.Equal(t, in.RunID, out.CallKeys[0].RunID)
	}
	out, err := rt.PlanResumeActivity(engine.WithActivityAttempt(t.Context(), 1), &parent)
	require.NoError(t, err)
	require.NotNil(t, out.ModelCallLimit)
	assert.Equal(t, parent.RunID, out.ModelCallLimit.Call.RunID)
	assert.Len(t, provider.calls, 2)
}
