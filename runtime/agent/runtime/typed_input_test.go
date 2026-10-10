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
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/require"
)

func TestPublishAwaitQueueItemPublishesTypedInput(t *testing.T) {
	t.Parallel()

	bus := &recordingHooks{}
	rt := &Runtime{
		RunEventStore: runloginmem.New(),
		Bus:           bus,
	}
	base := testWorkflowInput(t, rt, &planner.PlanInput{
		RunContext: run.Context{RootRunID: "test-root",
			RunID: "run-1",
		},
	})

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

	rt := New()
	base := testWorkflowInput(t, rt, &planner.PlanInput{})
	wf := &testWorkflowContext{ctx: context.Background(), finishRuntime: rt}
	wf.ensureSignals()
	wf.typedInputCh <- testTypedSignal(t, rt, base.RunContext.RootRunID, &api.TypedInputAnswer{
		RunID:   "run-1",
		ID:      "approval",
		Payload: rawjson.Message(`{"approved":true}`),
	})
	ctrl := interrupt.NewController(wf)

	results, err := waitAwaitTypedInput(wf.Context(), ctrl, &RunInput{AgentID: base.AgentID}, base, time.Second, &planner.AwaitTypedInput{
		ID:     "approval",
		Schema: rawjson.Message(`{"type":"object"}`),
	})
	require.NoError(t, err)
	require.Nil(t, results)
	require.Len(t, testView(t, rt, base).Typed, 1)
	require.Equal(t, "approval", testView(t, rt, base).Typed[0].ID)
	require.JSONEq(t, `{"approved":true}`, string(testTypedValue(t, rt, base)))
}

func TestWaitAwaitTypedInputRejectsInvalidPayload(t *testing.T) {
	t.Parallel()

	rt := New()
	base := testWorkflowInput(t, rt, &planner.PlanInput{})
	wf := &testWorkflowContext{ctx: context.Background(), finishRuntime: rt}
	wf.ensureSignals()
	wf.typedInputCh <- testTypedSignal(t, rt, base.RunContext.RootRunID, &api.TypedInputAnswer{
		RunID:   "run-1",
		ID:      "approval",
		Payload: rawjson.Message(`{"approved":`),
	})
	ctrl := interrupt.NewController(wf)

	_, err := waitAwaitTypedInput(wf.Context(), ctrl, &RunInput{AgentID: base.AgentID}, base, time.Second, &planner.AwaitTypedInput{
		ID:     "approval",
		Schema: rawjson.Message(`{"type":"object"}`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid answer payload")
}

func TestWaitAwaitTypedInputRejectsSchemaMismatch(t *testing.T) {
	t.Parallel()

	rt := New()
	base := testWorkflowInput(t, rt, &planner.PlanInput{})
	wf := &testWorkflowContext{ctx: context.Background(), finishRuntime: rt}
	wf.ensureSignals()
	wf.typedInputCh <- testTypedSignal(t, rt, base.RunContext.RootRunID, &api.TypedInputAnswer{
		RunID:   "run-1",
		ID:      "approval",
		Payload: rawjson.Message(`{"approved":"yes"}`),
	})
	ctrl := interrupt.NewController(wf)

	_, err := waitAwaitTypedInput(wf.Context(), ctrl, &RunInput{AgentID: base.AgentID}, base, time.Second, &planner.AwaitTypedInput{
		ID:     "approval",
		Schema: rawjson.Message(`{"type":"object","properties":{"approved":{"type":"boolean"}},"required":["approved"]}`),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "answer does not match schema")
}

func TestValidateTypedInputPayloadPreservesIntegerPrecision(t *testing.T) {
	for _, tc := range []struct {
		name    string
		schema  string
		valid   string
		invalid string
	}{
		{
			name:    "minimum",
			schema:  `{"type":"object","properties":{"n":{"type":"integer","minimum":9007199254740993}},"required":["n"]}`,
			valid:   `{"n":9007199254740993}`,
			invalid: `{"n":9007199254740992}`,
		},
		{
			name:    "enum",
			schema:  `{"type":"object","properties":{"n":{"type":"integer","enum":[9007199254740993]}},"required":["n"]}`,
			valid:   `{"n":9007199254740993}`,
			invalid: `{"n":9007199254740992}`,
		},
		{
			name:    "const",
			schema:  `{"type":"object","properties":{"n":{"type":"integer","const":9007199254740993}},"required":["n"]}`,
			valid:   `{"n":9007199254740993}`,
			invalid: `{"n":9007199254740992}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, validateTypedInputPayload(rawjson.Message(tc.schema), rawjson.Message(tc.valid)))
			require.Error(t, validateTypedInputPayload(rawjson.Message(tc.schema), rawjson.Message(tc.invalid)))
		})
	}
}

func TestValidateTypedInputPayloadRejectsInvalidJSON(t *testing.T) {
	validSchema := rawjson.Message(`{"type":"object"}`)
	for _, raw := range []string{`{"type":"object","type":"integer"}`, `{"type":"object"} {}`, "{\"description\":\"\xff\"}", `{"type":"object"`} {
		err := validateTypedInputPayload(rawjson.Message(raw), rawjson.Message(`{}`))
		require.Error(t, err, raw)
	}
	for _, raw := range []string{`{} {}`, `{"n":1,"n":2}`, "{\"n\":\"\xff\"}", `{"n":`} {
		err := validateTypedInputPayload(validSchema, rawjson.Message(raw))
		require.Error(t, err, raw)
	}
}

func TestWaitAwaitTypedInputTimesOut(t *testing.T) {
	t.Parallel()

	rt := New()
	base := testWorkflowInput(t, rt, &planner.PlanInput{})
	wf := &testWorkflowContext{ctx: context.Background(), finishRuntime: rt}
	ctrl := interrupt.NewController(wf)

	_, err := waitAwaitTypedInput(wf.Context(), ctrl, &RunInput{AgentID: base.AgentID}, base, time.Nanosecond, &planner.AwaitTypedInput{
		ID:     "approval",
		Schema: rawjson.Message(`{"type":"object"}`),
	})
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestBuildNextResumeRequestCarriesTypedInputs(t *testing.T) {
	t.Parallel()

	rt := &Runtime{}
	base := testWorkflowInput(t, rt, &planner.PlanInput{
		RunContext: run.Context{RootRunID: "test-root",
			RunID: "run-1",
		},
	})
	answer := testTypedSignal(t, rt, base.RunContext.RootRunID, &api.TypedInputAnswer{Payload: rawjson.Message(`{"approved":true}`)})
	require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.TypedInput, Hash: answer.PayloadRef, InputID: "approval"}))
	nextAttempt := 1

	req, err := rt.buildNextResumeRequest(agent.Ident("svc.agent"), base, &nextAttempt)
	require.NoError(t, err)
	require.Len(t, testPlannerView(t, rt, &req).Typed, 1)
	require.Equal(t, "approval", testPlannerView(t, rt, &req).Typed[0].ID)
	require.JSONEq(t, `{"approved":true}`, string(testTypedValue(t, rt, base)))
}
