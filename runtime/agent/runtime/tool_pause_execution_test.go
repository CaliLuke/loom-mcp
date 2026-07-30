package runtime

import (
	"context"
	"testing"
	"time"

	agent "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	runloginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	sessioninmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"

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

// TestRunLoopBudgetedToolPauseRecordsResultBeforeUserAnswer is the loom-mcp
// analogue of upstream v0.53.11's transcript-ordering regression. The
// invariant: when a budgeted tool returns a runtime-owned pause and the user
// later answers it, the resumed planner call must see a transcript in which
// the assistant tool_use, the durable user tool_result, and the user
// clarification answer appear in that exact order. Providers (Anthropic,
// OpenAI Responses) reject any other ordering as a broken tool_use /
// tool_result pairing.
//
// This drives `rt.runLoop` end-to-end so a future refactor that reorders
// handleToolTurn (await before recording) would flip the assertion. The
// structural argument alone is not enough — the test must catch reorder
// regressions, not just demonstrate helper behavior.
func TestRunLoopBudgetedToolPauseRecordsResultBeforeUserAnswer(t *testing.T) {
	rt := &Runtime{
		Bus:           noopHooks{},
		logger:        telemetry.NoopLogger{},
		metrics:       telemetry.NoopMetrics{},
		tracer:        telemetry.NoopTracer{},
		RunEventStore: runloginmem.New(),
		SessionStore:  sessioninmem.New(),
		toolSpecs: map[tools.Ident]tools.ToolSpec{
			tools.Ident("inline.ts.ask"): newAnyJSONSpec("inline.ts.ask", "inline.ts"),
		},
		toolsets: map[string]ToolsetRegistration{
			"inline.ts": {
				Inline:       true,
				DispatchMode: DispatchInline,
				Execute: func(ctx context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
					return &ToolExecutionResult{
						ToolResult: &planner.ToolResult{
							Name:       call.Name,
							ToolCallID: call.ToolCallID,
							Result:     map[string]any{"phase": "awaiting_input"},
						},
						Pause: &ToolPause{
							Clarification: &ToolPauseClarification{
								ID:       "clar-1",
								Question: "Which compressor should I investigate?",
							},
						},
					}, nil
				},
			},
		},
	}

	// The resume planner activity returns a FinalResponse so the run loop
	// terminates after a single pause/answer cycle.
	wfCtx := &testWorkflowContext{
		ctx:           context.Background(),
		hookRuntime:   rt,
		hasPlanResult: true,
		planResult: &planner.PlanResult{
			FinalResponse: &planner.FinalResponse{
				Message: &model.Message{
					Role:  model.ConversationRoleAssistant,
					Parts: []model.Part{model.TextPart{Text: "done"}},
				},
			},
		},
	}
	wfCtx.ensureSignals()
	ctrl := interrupt.NewController(wfCtx)
	wfCtx.clarifyCh <- &api.ClarificationAnswer{ID: "clar-1", Answer: "Compressor 1"}

	input := &RunInput{
		AgentID:   agent.Ident("svc.agent"),
		RunID:     "run-1",
		SessionID: "sess-1",
		TurnID:    "turn-1",
	}
	_, err := rt.CreateSession(context.Background(), input.SessionID)
	require.NoError(t, err)
	now := time.Now().UTC()
	require.NoError(t, rt.SessionStore.UpsertRun(context.Background(), session.RunMeta{
		AgentID:   string(input.AgentID),
		RunID:     input.RunID,
		SessionID: input.SessionID,
		Status:    session.RunStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}))
	base := &planner.PlanInput{
		RunContext: run.Context{RunID: input.RunID, SessionID: input.SessionID, TurnID: input.TurnID, Attempt: 1},
		Agent:      newAgentContext(agentContextOptions{runtime: rt, agentID: input.AgentID, runID: input.RunID}),
	}
	initial := &planner.PlanResult{
		ToolCalls: []planner.ToolRequest{{Name: tools.Ident("inline.ts.ask")}},
	}

	_, err = rt.runLoop(
		wfCtx,
		AgentRegistration{
			ID:                  input.AgentID,
			Planner:             &stubPlanner{},
			ExecuteToolActivity: "execute",
			ResumeActivityName:  "resume",
		},
		input, base, initial, nil, model.TokenUsage{},
		policy.CapsState{MaxToolCalls: 4, RemainingToolCalls: 4},
		time.Time{}, time.Time{}, 2, input.TurnID, nil, ctrl, 0,
	)
	require.NoError(t, err)

	// The resume planner call must have happened with the recorded transcript.
	require.Equal(t, "resume", wfCtx.lastPlannerCall.Name)
	require.NotNil(t, wfCtx.lastPlannerCall.Input)
	msgs := wfCtx.lastPlannerCall.Input.Messages
	require.NotEmpty(t, msgs, "resume planner call must see recorded transcript")

	// Locate the durable user tool_result and the user clarification answer in
	// the transcript handed to the resume planner.
	toolResultIdx := -1
	answerIdx := -1
	for i, m := range msgs {
		if m == nil || m.Role != model.ConversationRoleUser {
			continue
		}
		for _, p := range m.Parts {
			if _, ok := p.(model.ToolResultPart); ok {
				if toolResultIdx == -1 {
					toolResultIdx = i
				}
			}
			if tp, ok := p.(model.TextPart); ok && tp.Text == "Compressor 1" {
				answerIdx = i
			}
		}
	}
	require.NotEqual(t, -1, toolResultIdx, "resume planner must see the durable tool_result message")
	require.NotEqual(t, -1, answerIdx, "resume planner must see the clarification answer message")
	require.Less(t, toolResultIdx, answerIdx,
		"durable tool_result must be recorded BEFORE the clarification answer; "+
			"this is the v0.53.11 transcript-ordering invariant")
}
