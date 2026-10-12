package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAwaitRejectsMissingCallRefsBeforePrompts(t *testing.T) {
	for _, kind := range []string{"questions", "external"} {
		t.Run(kind, func(t *testing.T) {
			rt := New()
			ctx, cancel := context.WithCancel(t.Context())
			cancel() // A broken implementation must not hang waiting for input.
			wf := &testWorkflowContext{ctx: ctx, finishRuntime: rt}
			base := &workflowPlanInput{RunContext: run.Context{RunID: "run", RootRunID: "run"}}
			_, err := rt.handleAwaitQueue(wf, AgentRegistration{}, &RunInput{AgentID: "test"}, base, &runLoopState{}, engine.ActivityOptions{}, engine.ActivityOptions{}, 0, nil, interrupt.NewController(wf), &runDeadlines{}, "turn", nil, []planner.AwaitItem{rb7Await(kind, "await")}, nil)
			require.ErrorContains(t, err, "missing stored call")
			assert.Empty(t, wf.lastHookCall.Input, "invalid references must fail before publishing a prompt")
		})
	}
}

func TestWaitAwaitTypedInputRequiresWorkflowContext(t *testing.T) {
	wf := &testWorkflowContext{ctx: t.Context()}
	wf.ensureSignals()
	wf.typedInputCh <- &api.TypedInputAnswer{ID: "input", PayloadRef: "unused"}
	require.NotPanics(t, func() {
		_, err := waitAwaitTypedInput(t.Context(), interrupt.NewController(wf), &RunInput{AgentID: "test"}, &workflowPlanInput{}, time.Second, &planner.AwaitTypedInput{ID: "input", Schema: rawjson.Message(`{"type":"object"}`)})
		require.ErrorContains(t, err, "workflow context")
	})
}

func TestProvidedContentRetryKeepsEventIdentity(t *testing.T) {
	bus := &recordingHooks{}
	rt := New(WithHooks(bus))
	spec := newAnyJSONSpec("test.answer", "test")
	registerTestTool(t, rt, spec, nil)
	call := planner.ToolRequest{Name: spec.Name, ToolCallID: "call", Payload: rawjson.Message(`{}`)}
	stored, err := rt.storePlanActivityOutput(t.Context(), "root", &PlanActivityOutput{Result: &planner.PlanResult{ToolCalls: []planner.ToolRequest{call}}}, nil, nil)
	require.NoError(t, err)
	ref, err := rt.RunStore.Put(t.Context(), "root", []byte(`{"answer":"yes","another":1,"last":2}`))
	require.NoError(t, err)
	input := &api.RecordContentInput{RootRunID: "root", Provided: &api.ProvidedContent{TimestampMS: 12345,
		Calls: []planner.ToolRequest{call}, Results: []*api.ProvidedToolResult{{Name: call.Name, ToolCallID: call.ToolCallID, Refs: tools.ResultRefs{Result: ref}}},
		RunContext: run.Context{RootRunID: "root", RunID: "root"}, AgentID: "test", First: 2,
		CallRefs: map[string]runstore.Hash{"call": stored.ToolCallRefs[0]},
	}}
	first, err := rt.RecordContentActivity(t.Context(), input)
	require.NoError(t, err)
	second, err := rt.RecordContentActivity(t.Context(), input)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	require.Len(t, bus.events, 2, "observer delivery remains at least once")
	assert.Equal(t, int64(12345), bus.events[0].Timestamp())
	assert.Equal(t, bus.events[0].EventKey(), bus.events[1].EventKey(), "retry must keep the logical publication identity")
	page, err := rt.RunEventStore.List(t.Context(), "root", "", 100)
	require.NoError(t, err)
	assert.Len(t, page.Events, 1, "canonical result must be deduplicated")
	input.Provided.First = 4
	_, err = rt.RecordContentActivity(t.Context(), input)
	require.NoError(t, err)
	page, err = rt.RunEventStore.List(t.Context(), "root", "", 100)
	require.NoError(t, err)
	assert.Len(t, page.Events, 2, "a later await reusing the call ID is a new publication")
}
