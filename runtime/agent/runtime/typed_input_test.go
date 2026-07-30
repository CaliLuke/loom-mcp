package runtime

import (
	"context"
	"testing"
	"time"

	agent "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	runloginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	"github.com/stretchr/testify/require"
)

func TestPublishAwaitQueueItemPublishesTypedInput(t *testing.T) {
	t.Parallel()

	bus := &recordingHooks{}
	rt := &Runtime{
		RunEventStore: runloginmem.New(),
		Bus:           bus,
	}
	base := &planner.PlanInput{
		RunContext: run.Context{
			RunID: "run-1",
		},
	}

	err := rt.publishAwaitQueueItem(
		context.Background(),
		&RunInput{AgentID: agent.Ident("svc.agent")},
		base,
		&runLoopState{},
		"turn-1",
		planner.AwaitTypedInputItem(&planner.AwaitTypedInput{
			ID:     "approval",
			Title:  "Approval",
			Schema: rawjson.Message(`{"type":"object"}`),
		}),
		0,
	)
	require.NoError(t, err)
	require.Len(t, bus.events, 1)
	evt, ok := bus.events[0].(*hooks.AwaitTypedInputEvent)
	require.True(t, ok)
	require.Equal(t, "approval", evt.ID)
	require.Equal(t, "Approval", evt.Title)
	require.JSONEq(t, `{"type":"object"}`, string(evt.Schema))
}

func TestWaitAwaitTypedInputAppendsTypedInput(t *testing.T) {
	t.Parallel()

	wf := &testWorkflowContext{ctx: context.Background()}
	wf.ensureSignals()
	wf.typedInputCh <- &api.TypedInputAnswer{
		RunID:   "run-1",
		ID:      "approval",
		Payload: rawjson.Message(`{"approved":true}`),
	}
	ctrl := interrupt.NewController(wf)
	st := &runLoopState{}

	results, err := waitAwaitTypedInput(context.Background(), ctrl, st, time.Second, &planner.AwaitTypedInput{
		ID:     "approval",
		Schema: rawjson.Message(`{"type":"object"}`),
	})
	require.NoError(t, err)
	require.Nil(t, results)
	require.Len(t, st.TypedInputs, 1)
	require.Equal(t, "approval", st.TypedInputs[0].ID)
	require.JSONEq(t, `{"approved":true}`, string(st.TypedInputs[0].Payload))
}

func TestWaitAwaitTypedInputRejectsInvalidPayload(t *testing.T) {
	t.Parallel()

	wf := &testWorkflowContext{ctx: context.Background()}
	wf.ensureSignals()
	wf.typedInputCh <- &api.TypedInputAnswer{
		RunID:   "run-1",
		ID:      "approval",
		Payload: rawjson.Message(`{"approved":`),
	}
	ctrl := interrupt.NewController(wf)

	_, err := waitAwaitTypedInput(context.Background(), ctrl, &runLoopState{}, time.Second, &planner.AwaitTypedInput{
		ID:     "approval",
		Schema: rawjson.Message(`{"type":"object"}`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid answer payload")
}

func TestWaitAwaitTypedInputRejectsSchemaMismatch(t *testing.T) {
	t.Parallel()

	wf := &testWorkflowContext{ctx: context.Background()}
	wf.ensureSignals()
	wf.typedInputCh <- &api.TypedInputAnswer{
		RunID:   "run-1",
		ID:      "approval",
		Payload: rawjson.Message(`{"approved":"yes"}`),
	}
	ctrl := interrupt.NewController(wf)

	_, err := waitAwaitTypedInput(context.Background(), ctrl, &runLoopState{}, time.Second, &planner.AwaitTypedInput{
		ID:     "approval",
		Schema: rawjson.Message(`{"type":"object","properties":{"approved":{"type":"boolean"}},"required":["approved"]}`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "answer does not match schema")
}

func TestWaitAwaitTypedInputTimesOut(t *testing.T) {
	t.Parallel()

	wf := &testWorkflowContext{ctx: context.Background()}
	ctrl := interrupt.NewController(wf)

	_, err := waitAwaitTypedInput(context.Background(), ctrl, &runLoopState{}, time.Nanosecond, &planner.AwaitTypedInput{
		ID:     "approval",
		Schema: rawjson.Message(`{"type":"object"}`),
	})
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestBuildNextResumeRequestCarriesTypedInputs(t *testing.T) {
	t.Parallel()

	rt := &Runtime{}
	base := &planner.PlanInput{
		RunContext: run.Context{
			RunID: "run-1",
		},
	}
	nextAttempt := 1

	req, err := rt.buildNextResumeRequest(
		agent.Ident("svc.agent"),
		base,
		nil,
		[]planner.TypedInputOutput{{ID: "approval", Payload: rawjson.Message(`{"approved":true}`)}},
		&nextAttempt,
	)
	require.NoError(t, err)
	require.Len(t, req.TypedInputs, 1)
	require.Equal(t, "approval", req.TypedInputs[0].ID)
	require.JSONEq(t, `{"approved":true}`, string(req.TypedInputs[0].Payload))
}
