package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unknownReservationStore struct {
	runstore.Store
	failure  error
	failAt   int
	cancelAt int
	cancel   context.CancelFunc
}

func TestModelCallBudgetUnknownReservationConsumesSlot(t *testing.T) {
	failure := errors.New("allocation committed but response lost")
	rt, provider, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
		client, ok := agent.ModelClient("registered-model")
		require.True(t, ok)
		return budgetTestInvoke(ctx, client, false)
	})
	input.MaxModelCalls = 1
	rt.RunStore = unknownReservationStore{Store: rt.RunStore, failure: failure, failAt: 1}
	out, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), input)
	require.ErrorIs(t, err, failure)
	assert.Nil(t, out)
	out, err = rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 2), input)
	require.NoError(t, err)
	require.NotNil(t, out.ModelCallLimit)
	assert.Equal(t, 2, out.ModelCallLimit.Call.Seq)
	assert.Empty(t, out.CallKeys)
	assert.Empty(t, provider.calls)
}

func TestModelCallBudgetPreservesFailuresAfterRefusal(t *testing.T) {
	for _, kind := range []string{"cancel context", "return joined cancellation", "allocation failure"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("allocator unavailable after refusal")
			rt, provider, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
				client, ok := agent.ModelClient("registered-model")
				require.True(t, ok)
				require.NoError(t, budgetTestInvoke(ctx, client, false))
				refused := budgetTestInvoke(ctx, client, false)
				require.ErrorIs(t, refused, policy.ErrModelCallLimit)
				switch kind {
				case "cancel context":
					cancel()
				case "return joined cancellation":
					return errors.Join(refused, context.Canceled)
				case "allocation failure":
					require.ErrorIs(t, budgetTestInvoke(ctx, client, false), failure)
				}
				return nil
			})
			input.MaxModelCalls = 1
			if kind == "allocation failure" {
				rt.RunStore = unknownReservationStore{Store: rt.RunStore, failure: failure, failAt: 3}
			}
			out, err := rt.PlanStartActivity(engine.WithActivityAttempt(ctx, 1), input)
			if kind == "allocation failure" {
				require.ErrorIs(t, err, failure)
			} else {
				require.ErrorIs(t, err, context.Canceled)
			}
			assert.Nil(t, out)
			assert.Len(t, provider.calls, 1)
		})
	}
}

func TestModelCallBudgetInvalidActivityCap(t *testing.T) {
	rt, _, input := newCallInfoRuntime(t, func(context.Context, planner.PlannerContext) error {
		t.Error("invalid cap reached planner")
		return nil
	})
	input.MaxModelCalls = -1
	out, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), input)
	require.ErrorIs(t, err, ErrInvalidConfig)
	assert.Nil(t, out)
}

func TestModelCallBudgetFinalizerUsesSameReservations(t *testing.T) {
	rt, provider, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
		client, ok := agent.ModelClient("registered-model")
		require.True(t, ok)
		return budgetTestInvoke(ctx, client, false)
	})
	input.MaxModelCalls = 1
	out, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), input)
	require.NoError(t, err)
	require.Nil(t, out.ModelCallLimit)
	input.Finalize = &planner.Termination{Reason: planner.TerminationReasonTimeBudget}
	out, err = rt.PlanResumeActivity(engine.WithActivityAttempt(t.Context(), 1), input)
	require.NoError(t, err)
	require.NotNil(t, out.ModelCallLimit)
	assert.Equal(t, 2, out.ModelCallLimit.Call.Seq)
	assert.Nil(t, out.Result)
	assert.Empty(t, out.CallKeys)
	assert.Len(t, provider.calls, 1)
}

func TestModelCallBudgetSummaryPreservesCancellation(t *testing.T) {
	rt, input, provider, _ := summaryFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	input.MaxModelCalls = 1
	seq, err := rt.RunStore.NextSeq(ctx, input.Scope, input.RunID)
	require.NoError(t, err)
	require.Equal(t, 1, seq)
	rt.RunStore = unknownReservationStore{Store: rt.RunStore, cancelAt: 2, cancel: cancel}
	out, err := rt.SummarizeContextActivity(engine.WithActivityAttempt(ctx, 1), input)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, out)
	assert.Empty(t, provider.requests)
}

func (s unknownReservationStore) NextSeq(ctx context.Context, scope, runID string) (int, error) {
	seq, err := s.Store.NextSeq(ctx, scope, runID)
	if err != nil {
		return 0, err
	}
	if seq == s.cancelAt && s.cancel != nil {
		s.cancel()
	}
	if seq == s.failAt {
		return 0, s.failure
	}
	return seq, nil
}
