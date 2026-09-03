package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/interrupt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestRequiredCompletionToolFinishesWithoutPlannerResume(t *testing.T) {
	recorder := &recordingHooks{}
	rt := New(WithLogger(telemetry.NoopLogger{}), WithHooks(recorder))
	completion := newAnyJSONSpec("reports.persist", "reports")
	registerTestTool(t, rt, completion, func(call *planner.ToolRequest) *planner.ToolResult {
		return &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"saved": true}}
	})

	wfCtx, base, input := terminalPolicyRunInputs(t, rt)

	input.Policy = &PolicyOverrides{CompletionTool: completion.Name}
	out, err := rt.runLoop(
		wfCtx,
		AgentRegistration{ExecuteToolActivity: "execute", Specs: []tools.ToolSpec{completion}},
		input,
		base,
		&planner.PlanResult{
			ToolCalls: []planner.ToolRequest{{Name: completion.Name, Payload: rawjson.Message(`{}`)}},
			Notes:     []planner.PlannerAnnotation{{Text: "completion saved"}},
		},
		nil,
		model.TokenUsage{},
		policy.CapsState{MaxToolCalls: 1, RemainingToolCalls: 1},
		time.Time{},
		time.Time{},
		1,
		"turn-1",
		nil,
		nil,
		0,
	)

	require.NoError(t, err)
	require.NotNil(t, out)
	require.Nil(t, out.Final)
	require.NotNil(t, out.FinalToolResult)
	require.Equal(t, completion.Name, out.FinalToolResult.Name)
	require.Len(t, out.Notes, 1)
	require.Equal(t, "completion saved", out.Notes[0].Text)
	require.Equal(t, 1, countPlannerNoteEvents(recorder))
	require.Empty(t, wfCtx.lastPlannerCall.Name)
}

func TestRequiredCompletionToolRejectsPlannerFinalResponse(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	completion := newAnyJSONSpec("reports.persist", "reports")
	rt.toolSpecs[completion.Name] = completion
	wfCtx, base, input := terminalPolicyRunInputs(t, rt)

	input.Policy = &PolicyOverrides{CompletionTool: completion.Name}

	out, err := rt.runLoop(
		wfCtx,
		AgentRegistration{Specs: []tools.ToolSpec{completion}},
		input,
		base,
		&planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: newTextAgentMessage(model.ConversationRoleAssistant, "done")}},
		nil,
		model.TokenUsage{},
		policy.CapsState{MaxToolCalls: 1, RemainingToolCalls: 1},
		time.Time{},
		time.Time{},
		1,
		"turn-1",
		nil,
		nil,
		0,
	)

	require.Nil(t, out)
	require.ErrorContains(t, err, `completion tool "reports.persist" did not succeed`)
}

func TestCompletionToolRejectsQuestionAwait(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	completion := tools.Ident("reports.persist")
	result := &planner.PlanResult{Await: planner.NewAwait(
		planner.AwaitQuestionsItem(&planner.AwaitQuestions{
			ID: "await-1", ToolName: completion, ToolCallID: "persist-1", Payload: rawjson.Message(`{}`),
		}),
	)}

	err := rt.validateCompletionToolPlanResult(result, completion)

	require.ErrorContains(t, err, `completion tool "reports.persist" did not succeed`)
	require.ErrorContains(t, err, "delegated its execution to await work")
}

func TestLimitTerminalPlanExecutesDeterministicallyAfterDomainBudget(t *testing.T) {
	recorder := &recordingHooks{}
	rt := New(WithLogger(telemetry.NoopLogger{}), WithHooks(recorder))
	terminal := bookkeepingSpec("reports.limit", true)
	var executed planner.ToolRequest
	registerTestTool(t, rt, terminal, func(call *planner.ToolRequest) *planner.ToolResult {
		executed = *call
		return &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"limited": true}}
	})

	wfCtx, base, input := terminalPolicyRunInputs(t, rt)

	input.Labels = map[string]string{"tenant": "acme", FinalizationReasonLabel: "forged"}
	base.RunContext.Labels = map[string]string{"tenant": "acme", FinalizationReasonLabel: "forged"}

	input.Policy = &PolicyOverrides{LimitTerminalPlans: &LimitTerminalPlans{
		TimeBudget:  LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{"reason":"time"}`)},
		ToolCallCap: LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{"reason":"tools"}`)},
		RecoveryCap: LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{"reason":"recovery"}`)},
	}}
	domain := newAnyJSONSpec("domain.lookup", "domain")
	rt.toolSpecs[domain.Name] = domain

	out, err := rt.runLoop(
		wfCtx,
		AgentRegistration{ExecuteToolActivity: "execute", Specs: []tools.ToolSpec{terminal, domain}},
		input,
		base,
		&planner.PlanResult{
			ToolCalls: []planner.ToolRequest{{Name: domain.Name, Payload: rawjson.Message(`{}`)}},
			Notes:     []planner.PlannerAnnotation{{Text: "domain limit reached"}},
		},
		nil,
		model.TokenUsage{},
		policy.CapsState{MaxToolCalls: 1},
		time.Time{},
		time.Time{},
		2,
		"turn-1",
		nil,
		nil,
		0,
	)

	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.FinalToolResult)
	require.Equal(t, terminal.Name, out.FinalToolResult.Name)
	require.NotEmpty(t, executed.ToolCallID)
	require.Equal(t, "acme", executed.Labels["tenant"])
	require.Equal(t, string(planner.TerminationReasonToolCap), executed.Labels[FinalizationReasonLabel])
	require.Len(t, out.Notes, 1)
	require.Equal(t, "domain limit reached", out.Notes[0].Text)
	require.Equal(t, 1, countPlannerNoteEvents(recorder))
	require.Empty(t, wfCtx.lastPlannerCall.Name)
}

func TestPlannerFinalizerCanExecuteTerminalBookkeepingTool(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	terminal := bookkeepingSpec("reports.complete", true)
	domain := newAnyJSONSpec("domain.lookup", "domain")
	registerTestTool(t, rt, terminal, func(call *planner.ToolRequest) *planner.ToolResult {
		return &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"complete": true}}
	})
	rt.toolSpecs[domain.Name] = domain

	wfCtx, base, input := terminalPolicyRunInputs(t, rt)
	wfCtx.plannerOutputs = []*api.PlanActivityOutput{{
		Result: &planner.PlanResult{
			ToolCalls: []planner.ToolRequest{{
				Name: terminal.Name, Payload: rawjson.Message(`{}`),
			}},
			Notes: []planner.PlannerAnnotation{{Text: "limit persisted"}},
		},
	}}
	out, err := rt.runLoop(
		wfCtx,
		AgentRegistration{
			ResumeActivityName:  "resume",
			ExecuteToolActivity: "execute",
			Specs:               []tools.ToolSpec{domain, terminal},
		},
		input,
		base,
		&planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: domain.Name, Payload: rawjson.Message(`{}`)}}},
		nil,
		model.TokenUsage{},
		policy.CapsState{MaxToolCalls: 1},
		time.Time{},
		time.Time{},
		2,
		"turn-1",
		nil,
		nil,
		0,
	)

	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, out.FinalToolResult)
	require.Equal(t, terminal.Name, out.FinalToolResult.Name)
	require.Len(t, out.Notes, 1)
	require.Equal(t, "limit persisted", out.Notes[0].Text)
	require.Len(t, wfCtx.plannerCalls, 1)
	require.Equal(t, []tools.Ident{terminal.Name}, wfCtx.plannerCalls[0].Input.AllowedTools)
}

func TestFailedTerminalToolDoesNotReportSuccessfulCompletion(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	terminal := bookkeepingSpec("reports.complete", true)
	registerTestTool(t, rt, terminal, func(call *planner.ToolRequest) *planner.ToolResult {
		return &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Error: planner.NewToolError("write failed")}
	})

	wfCtx, base, input := terminalPolicyRunInputs(t, rt)

	out, err := rt.runLoop(
		wfCtx,
		AgentRegistration{ExecuteToolActivity: "execute", Specs: []tools.ToolSpec{terminal}},
		input,
		base,
		&planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: terminal.Name, Payload: rawjson.Message(`{}`)}}},
		nil,
		model.TokenUsage{},
		policy.CapsState{},
		time.Time{},
		time.Time{},
		1,
		"turn-1",
		nil,
		nil,
		0,
	)

	require.Nil(t, out)
	require.ErrorContains(t, err, `terminal tool "reports.complete" failed`)
}

func TestTerminalBatchDoesNotHideLaterFailure(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	first := bookkeepingSpec("reports.first", true)
	second := bookkeepingSpec("reports.second", true)
	rt.toolSpecs[first.Name] = first
	rt.toolSpecs[second.Name] = second

	terminal, err := rt.executedTerminalRunTool([]*planner.ToolResult{
		{Name: first.Name, Result: map[string]any{"saved": true}},
		{Name: second.Name, Error: planner.NewToolError("second write failed")},
	})

	require.False(t, terminal)
	require.ErrorContains(t, err, `terminal tool "reports.second" failed`)
}

func TestQuestionAwaitTerminalResultFinishesRun(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	terminal := bookkeepingSpec("reports.complete", true)
	rt.toolSpecs[terminal.Name] = terminal
	wfCtx, base, input := terminalPolicyRunInputs(t, rt)
	wfCtx.ensureSignals()
	wfCtx.toolResultsCh <- &api.ToolResultsSet{
		RunID: input.RunID,
		ID:    "await-1",
		Results: []*api.ProvidedToolResult{{
			Name: terminal.Name, ToolCallID: "complete-1", Result: rawjson.Message(`{"saved":true}`),
		}},
	}
	call := planner.ToolRequest{Name: terminal.Name, ToolCallID: "complete-1", Payload: rawjson.Message(`{}`)}
	ledger := transcript.NewLedger()
	st := &runLoopState{
		Result: &planner.PlanResult{Notes: []planner.PlannerAnnotation{{Text: "await completed"}}},
		Ledger: ledger,
	}
	rt.recordAssistantTurn(base, nil, []planner.ToolRequest{call}, ledger)
	ctrl := interrupt.NewController(wfCtx)
	item := planner.AwaitQuestionsItem(&planner.AwaitQuestions{
		ID: "await-1", ToolName: terminal.Name, ToolCallID: call.ToolCallID, Payload: call.Payload,
	})

	results, items, out, err := rt.collectAwaitResults(
		context.Background(),
		wfCtx,
		AgentRegistration{ID: input.AgentID, Specs: []tools.ToolSpec{terminal}},
		input,
		base,
		st,
		engine.ActivityOptions{},
		0,
		nil,
		"turn-1",
		ctrl,
		&runDeadlines{},
		nil,
		[]planner.AwaitItem{item},
		nil,
	)

	require.NoError(t, err)
	require.Empty(t, results)
	require.Empty(t, items)
	require.NotNil(t, out)
	require.Empty(t, base.Messages)
	require.Len(t, ledger.BuildMessages(), 2)
	require.Len(t, out.Notes, 1)
	require.Equal(t, "await completed", out.Notes[0].Text)
}

func TestCompletionAndTerminalToolsRejectPauses(t *testing.T) {
	tests := []struct {
		name   string
		spec   tools.ToolSpec
		policy *PolicyOverrides
		error  string
	}{
		{
			name:   "completion tool",
			spec:   newAnyJSONSpec("reports.persist", "reports"),
			policy: &PolicyOverrides{CompletionTool: "reports.persist"},
			error:  `completion tool "reports.persist" must not request a pause`,
		},
		{
			name:  "terminal tool",
			spec:  bookkeepingSpec("reports.complete", true),
			error: "terminal tool must not request a pause",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rt := New(WithLogger(telemetry.NoopLogger{}))
			registerPausedTestTool(t, rt, test.spec)
			wfCtx, base, input := terminalPolicyRunInputs(t, rt)
			input.Policy = test.policy

			out, err := rt.runLoop(
				wfCtx,
				AgentRegistration{ExecuteToolActivity: "execute", Specs: []tools.ToolSpec{test.spec}},
				input,
				base,
				&planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: test.spec.Name, Payload: rawjson.Message(`{}`)}}},
				nil,
				model.TokenUsage{},
				policy.CapsState{MaxToolCalls: 1, RemainingToolCalls: 1},
				time.Time{},
				time.Time{},
				1,
				"turn-1",
				nil,
				nil,
				0,
			)

			require.Nil(t, out)
			require.ErrorContains(t, err, test.error)
		})
	}
}

func TestFixedTerminalPlanRejectsPause(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	terminal := bookkeepingSpec("reports.limit", true)
	registerPausedTestTool(t, rt, terminal)
	wfCtx, base, input := terminalPolicyRunInputs(t, rt)
	input.Policy = &PolicyOverrides{LimitTerminalPlans: &LimitTerminalPlans{
		TimeBudget:  LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{"reason":"time"}`)},
		ToolCallCap: LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{"reason":"tools"}`)},
		RecoveryCap: LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{"reason":"recovery"}`)},
	}}
	domain := newAnyJSONSpec("domain.lookup", "domain")
	rt.toolSpecs[domain.Name] = domain

	out, err := rt.runLoop(
		wfCtx,
		AgentRegistration{ExecuteToolActivity: "execute", Specs: []tools.ToolSpec{terminal, domain}},
		input,
		base,
		&planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: domain.Name, Payload: rawjson.Message(`{}`)}}},
		nil,
		model.TokenUsage{},
		policy.CapsState{MaxToolCalls: 1},
		time.Time{},
		time.Time{},
		2,
		"turn-1",
		nil,
		nil,
		0,
	)

	require.Nil(t, out)
	require.ErrorContains(t, err, "finalization terminal tool must not request a pause")
}

func TestConfirmedTerminalToolFinishesOrRejectsPause(t *testing.T) {
	for _, paused := range []bool{false, true} {
		name := "finishes"
		if paused {
			name = "rejects pause"
		}
		t.Run(name, func(t *testing.T) {
			recorder := &recordingHooks{}
			rt := New(WithLogger(telemetry.NoopLogger{}), WithHooks(recorder))
			terminal := bookkeepingSpec("reports.complete", true)
			if paused {
				registerPausedTestTool(t, rt, terminal)
			} else {
				registerTestTool(t, rt, terminal, func(call *planner.ToolRequest) *planner.ToolResult {
					return &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"saved": true}}
				})
			}
			wfCtx, base, input := terminalPolicyRunInputs(t, rt)
			call := planner.ToolRequest{Name: terminal.Name, ToolCallID: "complete-1", Payload: rawjson.Message(`{}`)}
			ledger := transcript.NewLedger()
			st := &runLoopState{
				Result: &planner.PlanResult{Notes: []planner.PlannerAnnotation{{Text: "confirmed completion"}}},
				Ledger: ledger,
			}
			rt.recordAssistantTurn(base, nil, []planner.ToolRequest{call}, ledger)

			results, pauses, out, err := rt.executeConfirmedToolCall(
				context.Background(),
				wfCtx,
				AgentRegistration{ID: input.AgentID, ExecuteToolActivity: "execute", Specs: []tools.ToolSpec{terminal}},
				input,
				base,
				st,
				engine.ActivityOptions{},
				0,
				nil,
				"turn-1",
				&runDeadlines{},
				confirmationAwait{call: call},
			)

			if paused {
				require.Nil(t, out)
				require.ErrorContains(t, err, "terminal tool must not request a pause")
				return
			}
			require.NoError(t, err)
			require.Empty(t, results)
			require.Empty(t, pauses)
			require.NotNil(t, out)
			require.Len(t, out.Notes, 1)
			require.Equal(t, "confirmed completion", out.Notes[0].Text)
			require.Equal(t, 1, countPlannerNoteEvents(recorder))
		})
	}
}

func TestOrdinaryToolCallCannotReceiveFinalizationReasonLabel(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	base := &planner.PlanInput{RunContext: run.Context{
		RunID: "run-1", TurnID: "turn-1", Labels: map[string]string{FinalizationReasonLabel: "forged"},
	}}

	calls := rt.prepareAllowedCallsMetadata(
		"agent-1",
		base,
		[]planner.ToolRequest{{Name: "domain.lookup", Labels: map[string]string{FinalizationReasonLabel: "also-forged"}}},
		nil,
	)

	require.NotContains(t, calls[0].Labels, FinalizationReasonLabel)
	require.NotContains(t, base.RunContext.Labels, FinalizationReasonLabel)
}

func TestToolCallLabelsDoNotAliasRunOrSiblingLabels(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	base := &planner.PlanInput{RunContext: run.Context{
		RunID: "run-1", TurnID: "turn-1", Labels: map[string]string{"tenant": "acme"},
	}}

	calls := rt.prepareAllowedCallsMetadata(
		"agent-1",
		base,
		[]planner.ToolRequest{
			{Name: "domain.first", Labels: map[string]string{"scope": "first"}},
			{Name: "domain.second", Labels: map[string]string{"scope": "second"}},
		},
		nil,
	)

	require.Equal(t, map[string]string{"tenant": "acme"}, base.RunContext.Labels)
	require.Equal(t, map[string]string{"tenant": "acme", "scope": "first"}, calls[0].Labels)
	require.Equal(t, map[string]string{"tenant": "acme", "scope": "second"}, calls[1].Labels)
}

func TestCallerFinalizationLabelIsStrippedBeforeInitialPolicy(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	policyEngine := &capturingTerminalPolicy{}
	rt.Policy = policyEngine
	input := &RunInput{
		AgentID: "agent-1",
		RunID:   "run-1",
		TurnID:  "turn-1",
		Labels: map[string]string{
			"tenant": "acme", FinalizationReasonLabel: "forged",
		},
	}
	base := &planner.PlanInput{RunContext: workflowRunContext(input)}

	_, err := rt.preparePrePlanToolPolicy(
		context.Background(),
		AgentRegistration{ID: input.AgentID},
		input,
		base,
		policy.CapsState{},
		input.TurnID,
	)

	require.NoError(t, err)
	require.Equal(t, map[string]string{"tenant": "acme"}, policyEngine.input.RunContext.Labels)
	require.Equal(t, map[string]string{"tenant": "acme"}, policyEngine.input.Labels)
	require.Equal(t, map[string]string{"tenant": "acme"}, base.RunContext.Labels)
}

func TestFinalizationPolicyCannotObserveOrSpreadForgedLabels(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	first := bookkeepingSpec("reports.first", true)
	second := bookkeepingSpec("reports.second", true)
	rt.toolSpecs[first.Name] = first
	rt.toolSpecs[second.Name] = second
	policyEngine := &capturingTerminalPolicy{}
	rt.Policy = policyEngine
	base := &planner.PlanInput{RunContext: run.Context{
		RunID: "run-1", TurnID: "turn-1", Labels: map[string]string{
			"tenant": "acme", FinalizationReasonLabel: "forged-run",
		},
	}}
	input := &RunInput{AgentID: "agent-1"}
	calls := []planner.ToolRequest{
		{Name: first.Name, Payload: rawjson.Message(`{}`), Labels: map[string]string{
			"scope": "first", FinalizationReasonLabel: "forged-first",
		}},
		{Name: second.Name, Payload: rawjson.Message(`{}`), Labels: map[string]string{
			"scope": "second", FinalizationReasonLabel: "forged-second",
		}},
	}

	prepared, err := rt.prepareFinalizationToolCalls(
		context.Background(),
		AgentRegistration{ID: input.AgentID, Specs: []tools.ToolSpec{first, second}},
		input,
		base,
		calls,
		policy.CapsState{},
		"turn-1",
		1,
		planner.TerminationReasonToolCap,
	)

	require.NoError(t, err)
	require.NotContains(t, policyEngine.input.RunContext.Labels, FinalizationReasonLabel)
	require.Equal(t, map[string]string{"tenant": "acme"}, base.RunContext.Labels)
	require.Equal(t, map[string]string{
		"tenant": "acme", "scope": "first", FinalizationReasonLabel: string(planner.TerminationReasonToolCap),
	}, prepared[0].Labels)
	require.Equal(t, map[string]string{
		"tenant": "acme", "scope": "second", FinalizationReasonLabel: string(planner.TerminationReasonToolCap),
	}, prepared[1].Labels)
}

func TestResumeRecoveryFixedPlanPreservesPriorNotes(t *testing.T) {
	recorder := &recordingHooks{}
	rt := New(WithLogger(telemetry.NoopLogger{}), WithHooks(recorder))
	terminal := bookkeepingSpec("reports.limit", true)
	registerTestTool(t, rt, terminal, func(call *planner.ToolRequest) *planner.ToolResult {
		return &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"saved": true}}
	})
	wfCtx, base, input := terminalPolicyRunInputs(t, rt)
	input.Policy = &PolicyOverrides{LimitTerminalPlans: &LimitTerminalPlans{
		TimeBudget:  LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{"reason":"time"}`)},
		ToolCallCap: LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{"reason":"tools"}`)},
		RecoveryCap: LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{"reason":"recovery"}`)},
	}}
	st := &runLoopState{
		Result: &planner.PlanResult{Notes: []planner.PlannerAnnotation{{Text: "prior planner note"}}},
		Ledger: transcript.NewLedger(),
	}

	out, err := rt.handleResumePlanError(
		wfCtx,
		AgentRegistration{ID: input.AgentID, ExecuteToolActivity: "execute", Specs: []tools.ToolSpec{terminal}},
		input,
		base,
		st,
		&PlanActivityOutput{},
		&runDeadlines{},
		"turn-1",
		errRecoveryTurnCapExceeded,
	)

	require.NoError(t, err)
	require.NotNil(t, out)
	require.Len(t, out.Notes, 1)
	require.Equal(t, "prior planner note", out.Notes[0].Text)
	require.Equal(t, 1, countPlannerNoteEvents(recorder))
}

func TestInitialPlannerUsesActiveBudgetDeadline(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	wfCtx, _, input := terminalPolicyRunInputs(t, rt)
	wfCtx.plannerOutputs = []*api.PlanActivityOutput{{Result: &planner.PlanResult{
		FinalResponse: &planner.FinalResponse{Message: &model.Message{
			Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "done"}},
		}},
	}}}
	input.Policy = &PolicyOverrides{TimeBudget: 20 * time.Second, FinalizerGrace: 7 * time.Second}

	state, err := rt.startWorkflowRun(
		wfCtx,
		AgentRegistration{ID: input.AgentID, PlanActivityName: "plan"},
		input,
		workflowRunContext(input),
		"turn-1",
	)

	require.NoError(t, err)
	require.Equal(t, 20*time.Second, wfCtx.lastPlannerCall.Options.StartToCloseTimeout)
	require.Equal(t, 20*time.Second, wfCtx.lastPlannerCall.Options.ScheduleToStartTimeout)
	require.Equal(t, time.Unix(0, 0).Add(20*time.Second), state.deadlines.Budget)
	require.Equal(t, state.deadlines.Budget.Add(state.timing.FinalizerGrace), state.deadlines.Hard)
}

func TestInitialPlannerBudgetExpiryExecutesTimeTerminalPlan(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	terminal := bookkeepingSpec("reports.limit", true)
	var executed *planner.ToolRequest
	registerTestTool(t, rt, terminal, func(call *planner.ToolRequest) *planner.ToolResult {
		executed = call
		return &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"saved": true}}
	})
	baseCtx, base, input := terminalPolicyRunInputs(t, rt)
	input.Policy = &PolicyOverrides{LimitTerminalPlans: &LimitTerminalPlans{
		TimeBudget:  LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{"reason":"time"}`)},
		ToolCallCap: LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{"reason":"tools"}`)},
		RecoveryCap: LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{"reason":"recovery"}`)},
	}}
	deadlines := runDeadlines{
		Budget:         time.Unix(0, 0).Add(20 * time.Second),
		Hard:           time.Unix(0, 0).Add(27 * time.Second),
		FinalizerGrace: 7 * time.Second,
	}
	wfCtx := &fixedNowWorkflowContext{WorkflowContext: baseCtx, now: deadlines.Budget}

	out, status, err := rt.handleWorkflowStartError(
		wfCtx,
		AgentRegistration{ID: input.AgentID, ExecuteToolActivity: "execute", Specs: []tools.ToolSpec{terminal}},
		input,
		workflowStartState{
			planInput:   base,
			deadlines:   deadlines,
			caps:        policy.CapsState{},
			nextAttempt: 2,
		},
		"turn-1",
		context.DeadlineExceeded,
	)

	require.NoError(t, err)
	require.Equal(t, runStatusSuccess, status)
	require.NotNil(t, out)
	require.NotNil(t, executed)
	require.Equal(t, string(planner.TerminationReasonTimeBudget), executed.Labels[FinalizationReasonLabel])
}

func TestResumePlannerBudgetExpiryExecutesTimeTerminalPlan(t *testing.T) {
	recorder := &recordingHooks{}
	rt := New(WithLogger(telemetry.NoopLogger{}), WithHooks(recorder))
	terminal := bookkeepingSpec("reports.limit", true)
	var executed *planner.ToolRequest
	registerTestTool(t, rt, terminal, func(call *planner.ToolRequest) *planner.ToolResult {
		executed = call
		return &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"saved": true}}
	})
	baseCtx, base, input := terminalPolicyRunInputs(t, rt)
	input.Policy = &PolicyOverrides{LimitTerminalPlans: &LimitTerminalPlans{
		TimeBudget:  LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{"reason":"time"}`)},
		ToolCallCap: LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{"reason":"tools"}`)},
		RecoveryCap: LimitTerminalCall{Name: terminal.Name, Payload: rawjson.Message(`{"reason":"recovery"}`)},
	}}
	deadlines := runDeadlines{
		Budget:         time.Unix(0, 0).Add(20 * time.Second),
		Hard:           time.Unix(0, 0).Add(27 * time.Second),
		FinalizerGrace: 7 * time.Second,
	}
	wfCtx := &fixedNowWorkflowContext{WorkflowContext: baseCtx, now: deadlines.Budget}
	st := &runLoopState{
		Result: &planner.PlanResult{Notes: []planner.PlannerAnnotation{{Text: "prior planner note"}}},
		Ledger: transcript.NewLedger(),
	}

	out, err := rt.handleResumePlanError(
		wfCtx,
		AgentRegistration{ID: input.AgentID, ExecuteToolActivity: "execute", Specs: []tools.ToolSpec{terminal}},
		input,
		base,
		st,
		nil,
		&deadlines,
		"turn-1",
		context.DeadlineExceeded,
	)

	require.NoError(t, err)
	require.NotNil(t, out)
	require.NotNil(t, executed)
	require.Equal(t, string(planner.TerminationReasonTimeBudget), executed.Labels[FinalizationReasonLabel])
	require.Equal(t, "prior planner note", out.Notes[0].Text)
	require.Equal(t, 1, countPlannerNoteEvents(recorder))
}

func TestCompletionToolPolicyRejectsUnsafeConfigurations(t *testing.T) {
	t.Parallel()

	base := newAnyJSONSpec("reports.persist", "reports")
	bookkeeping := base
	bookkeeping.Bookkeeping = true
	terminal := base
	terminal.Bookkeeping = true
	terminal.TerminalRun = true
	confirmed := base
	confirmed.Confirmation = &tools.ConfirmationSpec{}

	tests := []struct {
		name  string
		spec  tools.ToolSpec
		input *RunInput
		error string
	}{
		{
			name:  "bookkeeping completion tool",
			spec:  bookkeeping,
			input: &RunInput{Policy: &PolicyOverrides{CompletionTool: base.Name}},
			error: "must be budgeted",
		},
		{
			name:  "terminal completion tool",
			spec:  terminal,
			input: &RunInput{Policy: &PolicyOverrides{CompletionTool: base.Name}},
			error: "must not be a terminal tool",
		},
		{
			name:  "confirmed completion tool",
			spec:  confirmed,
			input: &RunInput{Policy: &PolicyOverrides{CompletionTool: base.Name}},
			error: "cannot require confirmation",
		},
		{
			name: "workflow retry",
			spec: base,
			input: &RunInput{
				Policy:          &PolicyOverrides{CompletionTool: base.Name},
				WorkflowOptions: &WorkflowOptions{RetryPolicy: api.RetryPolicy{MaxAttempts: 2}},
			},
			error: "cannot configure whole-workflow retries",
		},
		{
			name: "limit terminal plans",
			spec: base,
			input: &RunInput{Policy: &PolicyOverrides{
				CompletionTool:     base.Name,
				LimitTerminalPlans: &LimitTerminalPlans{},
			}},
			error: "cannot be combined",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rt := New(WithLogger(telemetry.NoopLogger{}))
			err := rt.validateTerminalPolicies(AgentRegistration{ID: "reports.agent", Specs: []tools.ToolSpec{test.spec}}, test.input)
			require.ErrorContains(t, err, test.error)
		})
	}
}

func TestLimitTerminalCallSelectsConfiguredReason(t *testing.T) {
	t.Parallel()

	plans := LimitTerminalPlans{
		TimeBudget:  LimitTerminalCall{Name: "reports.time_limit", Payload: rawjson.Message(`{"reason":"time"}`)},
		ToolCallCap: LimitTerminalCall{Name: "reports.tool_limit", Payload: rawjson.Message(`{"reason":"tools"}`)},
		RecoveryCap: LimitTerminalCall{Name: "reports.recovery_limit", Payload: rawjson.Message(`{"reason":"recovery"}`)},
	}
	tests := []struct {
		name   string
		reason planner.TerminationReason
		want   LimitTerminalCall
	}{
		{name: "time budget", reason: planner.TerminationReasonTimeBudget, want: plans.TimeBudget},
		{name: "tool cap", reason: planner.TerminationReasonToolCap, want: plans.ToolCallCap},
		{name: "recovery cap", reason: planner.TerminationReasonFailureCap, want: plans.RecoveryCap},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			call, ok, err := limitTerminalCall(&plans, test.reason)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, test.want, call)
		})
	}
}

func TestFinalizationToolCallIDsAreDeterministic(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	terminal := bookkeepingSpec("reports.limit", true)
	registerTestTool(t, rt, terminal, func(call *planner.ToolRequest) *planner.ToolResult {
		return &planner.ToolResult{Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"limited": true}}
	})
	_, base, input := terminalPolicyRunInputs(t, rt)
	reg := AgentRegistration{ID: input.AgentID, Specs: []tools.ToolSpec{terminal}}
	calls := []planner.ToolRequest{{Name: terminal.Name, Payload: rawjson.Message(`{}`)}}

	first, err := rt.prepareFinalizationToolCalls(
		context.Background(), reg, input, base, append([]planner.ToolRequest(nil), calls...), policy.CapsState{}, "turn-1", 4, planner.TerminationReasonToolCap,
	)
	require.NoError(t, err)
	second, err := rt.prepareFinalizationToolCalls(
		context.Background(), reg, input, base, append([]planner.ToolRequest(nil), calls...), policy.CapsState{}, "turn-1", 4, planner.TerminationReasonToolCap,
	)
	require.NoError(t, err)

	require.NotEmpty(t, first[0].ToolCallID)
	require.Equal(t, first[0].ToolCallID, second[0].ToolCallID)
}

func TestTerminalRunOptionsCopyPolicyInputs(t *testing.T) {
	t.Parallel()

	plans := LimitTerminalPlans{
		TimeBudget:  LimitTerminalCall{Name: "reports.limit", Payload: rawjson.Message(`{"reason":"time"}`)},
		ToolCallCap: LimitTerminalCall{Name: "reports.limit", Payload: rawjson.Message(`{"reason":"tools"}`)},
		RecoveryCap: LimitTerminalCall{Name: "reports.limit", Payload: rawjson.Message(`{"reason":"recovery"}`)},
	}
	input := &RunInput{}
	WithLimitTerminalPlans(plans)(input)
	WithRunCompletionTool("reports.persist")(input)
	plans.TimeBudget.Payload[0] = '['

	require.Equal(t, tools.Ident("reports.persist"), input.Policy.CompletionTool)
	require.JSONEq(t, `{"reason":"time"}`, string(input.Policy.LimitTerminalPlans.TimeBudget.Payload))
}

type fixedNowWorkflowContext struct {
	engine.WorkflowContext
	now time.Time
}

func (c *fixedNowWorkflowContext) Now() time.Time {
	return c.now
}

type capturingTerminalPolicy struct {
	input policy.Input
}

func (p *capturingTerminalPolicy) Decide(_ context.Context, input policy.Input) (policy.Decision, error) {
	p.input = input
	allowed := make([]tools.Ident, 0, len(input.Tools))
	for _, tool := range input.Tools {
		allowed = append(allowed, tool.ID)
	}
	return policy.Decision{AllowedTools: allowed}, nil
}

func countPlannerNoteEvents(recorder *recordingHooks) int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	count := 0
	for _, event := range recorder.events {
		if event.Type() == hooks.PlannerNote {
			count++
		}
	}
	return count
}

func registerTestTool(t *testing.T, rt *Runtime, spec tools.ToolSpec, result func(*planner.ToolRequest) *planner.ToolResult) {
	t.Helper()
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{
		Name: spec.Toolset,
		Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			return Executed(result(call)), nil
		},
		Specs: []tools.ToolSpec{spec},
	}))
}

func registerPausedTestTool(t *testing.T, rt *Runtime, spec tools.ToolSpec) {
	t.Helper()
	require.NoError(t, rt.RegisterToolset(ToolsetRegistration{
		Name: spec.Toolset,
		Execute: func(_ context.Context, call *planner.ToolRequest) (*ToolExecutionResult, error) {
			return &ToolExecutionResult{
				ToolResult: &planner.ToolResult{
					Name: call.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"saved": true},
				},
				Pause: &ToolPause{
					Clarification: &ToolPauseClarification{ID: "clarify-1", Question: "Which value?"},
				},
			}, nil
		},
		Specs: []tools.ToolSpec{spec},
	}))
}

func terminalPolicyRunInputs(t *testing.T, rt *Runtime) (*testWorkflowContext, *planner.PlanInput, *RunInput) {
	t.Helper()
	ctx := context.Background()
	_, err := rt.CreateSession(ctx, "sess-1")
	require.NoError(t, err)
	require.NoError(t, rt.SessionStore.UpsertRun(ctx, session.RunMeta{
		AgentID: "agent-1", RunID: "run-1", SessionID: "sess-1", Status: session.RunStatusRunning,
	}))
	wfCtx := &testWorkflowContext{ctx: ctx, runtime: rt}
	base := &planner.PlanInput{RunContext: run.Context{
		RunID: "run-1", SessionID: "sess-1", TurnID: "turn-1", Attempt: 1,
	}}
	input := &RunInput{
		AgentID: agent.Ident("agent-1"), RunID: "run-1", SessionID: "sess-1", TurnID: "turn-1",
	}
	return wfCtx, base, input
}
