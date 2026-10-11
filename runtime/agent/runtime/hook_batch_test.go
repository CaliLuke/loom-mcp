package runtime

import (
	"context"
	"errors"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlannerStepBatchPreservesCanonicalOrder(t *testing.T) {
	intercepted := 0
	var rt *Runtime
	rt = New(WithInterceptors(RuntimeInterceptorFuncs{BeforeEventFunc: func(_ context.Context, input *BeforeEventInput) (*BeforeEventDecision, error) {
		intercepted++
		assert.Equal(t, hooks.RunPhaseChanged, input.Event.Type())
		page, err := rt.RunEventStore.List(t.Context(), "run", "", 10)
		require.NoError(t, err)
		require.Len(t, page.Events, 1)
		assert.Equal(t, hooks.PlannerStep, page.Events[0].Type)
		return &BeforeEventDecision{Drop: true}, nil
	}}))
	wf := &testWorkflowContext{ctx: t.Context(), hookRuntime: rt}
	require.NoError(t, rt.publishWorkflowHookBatch(wf, "turn", hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{}, nil, api.WorkflowProtocolVersion, time.Unix(42, 0)), hooks.NewRunPhaseChangedEvent("run", "agent", "", run.PhaseExecutingTools)))
	assert.Equal(t, 1, intercepted)
}

func TestPlannerStepBatchProjectionFailureStillPublishesPhase(t *testing.T) {
	bus := hooks.NewBus()
	registration, err := bus.Register(hooks.SubscriberFunc(func(_ context.Context, event hooks.Event) error {
		if event.Type() == hooks.PlannerStep {
			return errors.New("projection unavailable")
		}
		return nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, registration.Close())
	})
	rt := New(WithHooks(bus))
	wf := &testWorkflowContext{ctx: t.Context(), hookRuntime: rt}
	require.NoError(t, rt.publishWorkflowHookBatch(wf, "turn", hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{}, nil, api.WorkflowProtocolVersion, time.Unix(42, 0)), hooks.NewRunPhaseChangedEvent("run", "agent", "", run.PhaseExecutingTools)))
	page, err := rt.RunEventStore.List(t.Context(), "run", "", 10)
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	assert.Equal(t, hooks.PlannerStep, page.Events[0].Type)
	assert.Equal(t, hooks.RunPhaseChanged, page.Events[1].Type)
}

func TestPlannerStepBatchAppendFailureStopsFollowingEvent(t *testing.T) {
	failure := errors.New("canonical append unavailable")
	log := &recordingRunlog{err: failure}
	intercepted := 0
	rt := New(WithRunEventStore(log), WithInterceptors(RuntimeInterceptorFuncs{BeforeEventFunc: func(context.Context, *BeforeEventInput) (*BeforeEventDecision, error) {
		intercepted++
		return nil, nil
	}}))
	wf := &testWorkflowContext{ctx: t.Context(), hookRuntime: rt}
	err := rt.publishWorkflowHookBatch(wf, "turn", hooks.NewPlannerStepEvent("run", "agent", "", 1, index.ViewRef{}, nil, api.WorkflowProtocolVersion, time.Unix(42, 0)), hooks.NewRunPhaseChangedEvent("run", "agent", "", run.PhaseExecutingTools))
	require.ErrorIs(t, err, failure)
	assert.Zero(t, intercepted)
}

func TestHookBatchRejectsEmptyAndMalformedInput(t *testing.T) {
	rt := New()
	wf := &testWorkflowContext{ctx: t.Context(), hookRuntime: rt}
	require.ErrorContains(t, rt.publishWorkflowHookBatch(wf, "turn"), "requires events")
	require.ErrorContains(t, rt.publishWorkflowHookBatch(wf, "turn", nil), "event is required")
	for _, data := range []string{"[]", "[null]", "{"} {
		require.Error(t, rt.hookBatchActivity(t.Context(), &HookActivityInput{Payload: []byte(data)}))
	}
}
