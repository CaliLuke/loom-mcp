package runtime

import (
	"context"
	"testing"
	"time"

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	runloginmem "github.com/CaliLuke/loom-mcp/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/runtime/agent/transcript"

	"github.com/stretchr/testify/require"
)

// TestExecuteToolCalls_RuntimeOwnedPauseFlowsThroughBatch covers the v0.52.0
// ToolExecutionResult envelope: when an inline tool returns both a durable
// ToolResult and a runtime-owned Pause clarification, the batch must surface
// both via the new helper extractors so the workflow can append the durable
// result to ToolOutputs / transcript while projecting the pause into the await
// queue. The Pause itself must never land in cumulative ToolOutputs history.
func TestExecuteToolCalls_RuntimeOwnedPauseFlowsThroughBatch(t *testing.T) {
	recorder := &recordingHooks{}
	rt := &Runtime{
		toolsets: map[string]ToolsetRegistration{
			"inline.ts": {
				Inline:       true,
				DispatchMode: DispatchInline,
				Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
					return &ToolExecutionResult{
						ToolResult: &planner.ToolResult{
							Name:       call.Name,
							ToolCallID: call.ToolCallID,
							Result:     map[string]any{"durable": true},
						},
						Pause: &ToolPause{
							Clarification: &ToolPauseClarification{
								ID:       "pause-1",
								Question: "Which one did you mean?",
							},
						},
					}, nil
				},
			},
		},
		toolSpecs: map[tools.Ident]tools.ToolSpec{
			tools.Ident("inline.ts.ask"): newAnyJSONSpec("inline.ts.ask", "inline.ts"),
		},
		logger:        telemetry.NoopLogger{},
		metrics:       telemetry.NoopMetrics{},
		tracer:        telemetry.NoopTracer{},
		RunEventStore: runloginmem.New(),
		Bus:           recorder,
	}

	wfCtx := &testWorkflowContext{
		ctx:         context.Background(),
		hookRuntime: rt,
	}

	runCtx := &run.Context{
		RunID:     "run-1",
		SessionID: "sess-1",
		TurnID:    "turn-1",
	}

	call := planner.ToolRequest{
		Name:       tools.Ident("inline.ts.ask"),
		RunID:      runCtx.RunID,
		SessionID:  runCtx.SessionID,
		TurnID:     runCtx.TurnID,
		ToolCallID: "tc-1",
	}

	outcomes, timedOut, err := rt.executeToolCalls(
		wfCtx, "execute", engine.ActivityOptions{}, agent.Ident("agent-1"), runCtx,
		nil, []planner.ToolRequest{call}, 0, nil, time.Time{},
	)
	require.NoError(t, err)
	require.False(t, timedOut)
	require.Len(t, outcomes, 1)

	// The ToolExecutionResult envelope carries both halves.
	require.NotNil(t, outcomes[0].ToolResult)
	require.Equal(t, "tc-1", outcomes[0].ToolResult.ToolCallID)
	require.Equal(t, map[string]any{"durable": true}, outcomes[0].ToolResult.Result)
	require.NotNil(t, outcomes[0].Pause)
	require.NotNil(t, outcomes[0].Pause.Clarification)
	require.Equal(t, "pause-1", outcomes[0].Pause.Clarification.ID)

	// toolResultsFromExecutions yields only the durable planner-visible result.
	vals := toolResultsFromExecutions(outcomes)
	require.Len(t, vals, 1)
	require.Equal(t, "tc-1", vals[0].ToolCallID)
	require.Equal(t, map[string]any{"durable": true}, vals[0].Result)

	// toolPausesFromExecutions yields the current-batch pause signals.
	pauses := toolPausesFromExecutions(outcomes)
	require.Len(t, pauses, 1)
	require.Equal(t, "pause-1", pauses[0].Clarification.ID)

	// toolPauseAwaitItems projects the pause into a clarification await item.
	awaitItems, err := toolPauseAwaitItems(pauses)
	require.NoError(t, err)
	require.Len(t, awaitItems, 1)
	require.Equal(t, planner.AwaitItemKindClarification, awaitItems[0].Kind)
	require.NotNil(t, awaitItems[0].Clarification)
	require.Equal(t, "pause-1", awaitItems[0].Clarification.ID)
	require.Equal(t, "Which one did you mean?", awaitItems[0].Clarification.Question)

	// The hook bus saw the durable result event; that durable result is what the
	// downstream transcript / runlog projection consumes.
	var resultEvents []*hooks.ToolResultReceivedEvent
	for _, evt := range recorder.events {
		if e, ok := evt.(*hooks.ToolResultReceivedEvent); ok {
			resultEvents = append(resultEvents, e)
		}
	}
	require.Len(t, resultEvents, 1)
	require.Equal(t, "tc-1", resultEvents[0].ToolCallID)
}

// TestApplyExecutedToolTurn_DoesNotPersistPauseIntoToolOutputs verifies that
// the cumulative ToolOutputs state appended after a tool turn contains only
// the durable planner-visible result, not the runtime-owned pause envelope.
// A pause is a current-batch signal: replaying it into cumulative history
// would re-trigger await projection on subsequent turns.
func TestApplyExecutedToolTurn_DoesNotPersistPauseIntoToolOutputs(t *testing.T) {
	rt := &Runtime{
		toolSpecs: map[tools.Ident]tools.ToolSpec{
			tools.Ident("inline.ts.ask"): newAnyJSONSpec("inline.ts.ask", "inline.ts"),
		},
		logger:        telemetry.NoopLogger{},
		metrics:       telemetry.NoopMetrics{},
		tracer:        telemetry.NoopTracer{},
		RunEventStore: runloginmem.New(),
	}

	call := planner.ToolRequest{
		Name:       tools.Ident("inline.ts.ask"),
		ToolCallID: "tc-pause",
		RunID:      "run-1",
		SessionID:  "sess-1",
		TurnID:     "turn-1",
	}
	outcomes := []*ToolExecutionResult{
		{
			ToolResult: &planner.ToolResult{
				Name:       call.Name,
				ToolCallID: call.ToolCallID,
				Result:     map[string]any{"durable": true},
			},
			Pause: &ToolPause{
				Clarification: &ToolPauseClarification{ID: "p-1", Question: "?"},
			},
		},
	}

	base := &planner.PlanInput{}
	st := &runLoopState{Ledger: transcript.NewLedger()}

	vals := toolResultsFromExecutions(outcomes)
	require.NoError(t, applyExecutedToolTurn(context.Background(), rt, base, st, []planner.ToolRequest{call}, vals))

	// ToolOutputs holds exactly the durable result. There is no pause payload.
	require.Len(t, st.ToolOutputs, 1)
	require.Equal(t, "tc-pause", st.ToolOutputs[0].ToolCallID)

	// ToolEvents (the cumulative planner-facing audit trail) also holds only
	// the durable shape.
	require.Len(t, st.ToolEvents, 1)
	require.Equal(t, "tc-pause", st.ToolEvents[0].ToolCallID)
}

// TestToolPauseAwaitItems_RejectsInvalidPauses guards the contract that every
// pause projected into the await queue must carry a clarification payload.
func TestToolPauseAwaitItems_RejectsInvalidPauses(t *testing.T) {
	_, err := toolPauseAwaitItems([]*ToolPause{nil})
	require.Error(t, err)

	_, err = toolPauseAwaitItems([]*ToolPause{{Clarification: nil}})
	require.Error(t, err)
}
