package runtime

import (
	"context"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

func TestUnknownToolOutcomeFinalizesWithoutOrdinaryResume(t *testing.T) {
	tool := tools.Ident("svc.effect")
	spec := newAnyJSONSpec(tool, "svc.tools")
	rt := &Runtime{
		Bus: noopHooks{}, RunEventStore: inmem.New(), logger: telemetry.NoopLogger{},
		metrics: telemetry.NoopMetrics{}, tracer: telemetry.NoopTracer{},
		toolSpecs: map[tools.Ident]tools.ToolSpec{tool: spec},
	}
	final := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "done"}}}
	wfCtx := &testWorkflowContext{
		ctx: context.Background(),
		asyncResult: ToolOutput{
			Error:     "outcome_unknown: execution outcome is unknown; do not retry",
			ErrorKind: planner.ToolErrorKindOutcomeUnknown,
		},
		hasPlanResult: true,
		planResult:    &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: final}},
	}
	input := &RunInput{AgentID: "svc.agent", RunID: "run-unknown"}
	base := &planner.PlanInput{RunContext: run.Context{RunID: input.RunID}}
	reg := AgentRegistration{
		ID: input.AgentID, Planner: &stubPlanner{}, ExecuteToolActivity: "execute", ResumeActivityName: "resume",
		Specs: []tools.ToolSpec{spec}, Policy: RunPolicy{MaxRecoveryTurns: 1},
	}
	initial := &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: tool, Payload: []byte(`{}`)}}}
	out, err := rt.runLoop(wfCtx, reg, input, base, initial, nil, model.TokenUsage{}, initialCaps(reg.Policy), time.Time{}, time.Time{}, 2, "turn-1", nil, nil, 0)

	require.NoError(t, err)
	require.NotNil(t, out)
	require.Len(t, wfCtx.plannerCalls, 1)
	call := wfCtx.plannerCalls[0]
	require.Equal(t, "resume", call.Name)
	require.NotNil(t, call.Input)
	require.NotNil(t, call.Input.Finalize)
	require.Equal(t, planner.TerminationReasonOutcomeUnknown, call.Input.Finalize.Reason)
	require.Empty(t, call.Input.AllowedTools)
	require.Len(t, call.Input.ToolOutputs, 1)
	require.Equal(t, planner.ToolErrorKindOutcomeUnknown, call.Input.ToolOutputs[0].Error.Kind)
}

func TestOutcomeUnknownClassificationUsesTypedField(t *testing.T) {
	unknown := planner.NewToolError("outcome_unknown: route lost")
	unknown.Kind = planner.ToolErrorKindOutcomeUnknown
	ordinary := planner.NewToolError("outcome_unknown: route lost")

	require.True(t, resultsContainOutcomeUnknown([]*planner.ToolResult{{Error: unknown}}))
	require.False(t, resultsContainOutcomeUnknown([]*planner.ToolResult{{Error: ordinary}}))
	require.False(t, resultsContainOutcomeUnknown([]*planner.ToolResult{{Error: planner.NewToolError("ordinary failure")}}))
	require.True(t, resultsRequireRecovery([]*planner.ToolResult{{Error: ordinary}}))
	require.False(t, resultsRequireRecovery([]*planner.ToolResult{{
		Error:     planner.NewToolError("rate limited"),
		RetryHint: &planner.RetryHint{Reason: planner.RetryReasonRateLimited},
	}}))
}

func TestOutcomeUnknownFinalizerRejectsToolCalls(t *testing.T) {
	output := &PlanActivityOutput{Result: &planner.PlanResult{
		ToolCalls: []planner.ToolRequest{{Name: "svc.effect", Payload: []byte(`{}`)}},
	}}

	err := validateFinalizePlanOutput(output, "unknown outcome", planner.TerminationReasonOutcomeUnknown)

	require.ErrorContains(t, err, "tool calls are not allowed after an unknown execution outcome")
}

func TestAwaitContinuationFinalizesWhenHistoryHasUnknownOutcome(t *testing.T) {
	rt := New()
	final := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "done"}}}
	wfCtx := &testWorkflowContext{
		ctx:           context.Background(),
		hasPlanResult: true,
		planResult:    &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: final}},
	}
	input := &RunInput{AgentID: "svc.agent", RunID: "run-unknown-await"}
	base := &planner.PlanInput{RunContext: run.Context{RunID: input.RunID}}
	reg := AgentRegistration{ID: input.AgentID, ResumeActivityName: "resume"}
	st := &runLoopState{
		Caps: policy.CapsState{MaxRecoveryTurns: 2, RemainingRecoveryTurns: 2},
		ToolOutputs: []*planner.ToolOutput{{Error: planner.NewToolErrorWithKind(
			"outcome is unknown", planner.ToolErrorKindOutcomeUnknown,
		)}},
	}

	out, err := rt.resumeAfterToolTurn(
		wfCtx, reg, input, base, st, engine.ActivityOptions{}, &runDeadlines{}, "turn-await",
	)

	require.NoError(t, err)
	require.NotNil(t, out)
	require.Len(t, wfCtx.plannerCalls, 1)
	call := wfCtx.plannerCalls[0]
	require.NotNil(t, call.Input.Finalize)
	require.Equal(t, planner.TerminationReasonOutcomeUnknown, call.Input.Finalize.Reason)
	require.Empty(t, call.Input.AllowedTools)
}

func TestUnknownOutcomeKindSurvivesActivityJSONRoundTrip(t *testing.T) {
	tool := tools.Ident("svc.effect")
	spec := newAnyJSONSpec(tool, "svc.tools")
	rt := &Runtime{toolSpecs: map[tools.Ident]tools.ToolSpec{tool: spec}}
	result := &planner.ToolResult{
		Name: tool, ToolCallID: "call-1",
		Error: planner.NewToolErrorWithKind("execution outcome is unknown", planner.ToolErrorKindOutcomeUnknown),
	}
	activityOutput := newToolActivityOutput(nil, result)

	payload, err := json.Marshal(activityOutput)
	require.NoError(t, err)
	var decodedActivityOutput ToolOutput
	require.NoError(t, json.Unmarshal(payload, &decodedActivityOutput))

	decodedResult, err := (&toolBatchExec{r: rt}).decodeActivityToolResult(
		context.Background(),
		futureInfo{call: planner.ToolRequest{Name: tool, ToolCallID: "call-1"}},
		&decodedActivityOutput,
	)
	require.NoError(t, err)
	require.NotNil(t, decodedResult.Error)
	require.Equal(t, planner.ToolErrorKindOutcomeUnknown, decodedResult.Error.Kind)
}

func TestUnknownOutcomeSkipsRecoveryCapAndRejectsFinalizerToolCall(t *testing.T) {
	effectTool := tools.Ident("svc.effect")
	recoveryTool := tools.Ident("svc.recovery")
	effectSpec := newAnyJSONSpec(effectTool, "svc.tools")
	recoverySpec := newAnyJSONSpec(recoveryTool, "svc.tools")
	recoverySpec.Bookkeeping = true
	recoverySpec.TerminalRun = true
	rt := &Runtime{toolSpecs: map[tools.Ident]tools.ToolSpec{
		effectTool:   effectSpec,
		recoveryTool: recoverySpec,
	}}
	unknown := planner.NewToolErrorWithKind("execution outcome is unknown", planner.ToolErrorKindOutcomeUnknown)
	result := &planner.ToolResult{Name: effectTool, ToolCallID: "call-1", Error: unknown}
	toolOutput := &planner.ToolOutput{Name: effectTool, ToolCallID: "call-1", Error: unknown}
	wfCtx := &testWorkflowContext{
		ctx:           context.Background(),
		hasPlanResult: true,
		planResult: &planner.PlanResult{ToolCalls: []planner.ToolRequest{{
			Name: effectTool, ToolCallID: "replacement-call", Payload: []byte(`{}`),
		}}},
	}
	input := &RunInput{AgentID: "svc.agent", RunID: "run-unknown-terminal", Policy: &PolicyOverrides{
		LimitTerminalPlans: &LimitTerminalPlans{
			TimeBudget:  LimitTerminalCall{Name: recoveryTool, Payload: []byte(`{}`)},
			ToolCallCap: LimitTerminalCall{Name: recoveryTool, Payload: []byte(`{}`)},
			RecoveryCap: LimitTerminalCall{Name: recoveryTool, Payload: []byte(`{}`)},
		},
	}}
	base := &planner.PlanInput{RunContext: run.Context{RunID: input.RunID}}
	reg := AgentRegistration{ID: input.AgentID, ResumeActivityName: "resume", Specs: []tools.ToolSpec{effectSpec, recoverySpec}}
	require.NoError(t, rt.validateLimitTerminalPlans(reg, input.Policy.LimitTerminalPlans))

	out, err := rt.finalizeWithPlanner(
		wfCtx, reg, input, base, []*planner.ToolResult{result}, []*planner.ToolOutput{toolOutput},
		model.TokenUsage{}, policy.CapsState{}, 1, "turn-1", nil,
		planner.TerminationReasonFailureCap, time.Time{},
	)

	require.Nil(t, out)
	require.ErrorContains(t, err, "outside the current recovery catalog")
	require.Len(t, wfCtx.plannerCalls, 1)
	call := wfCtx.plannerCalls[0]
	require.Equal(t, planner.TerminationReasonOutcomeUnknown, call.Input.Finalize.Reason)
	require.Empty(t, call.Input.AllowedTools)
	require.Empty(t, wfCtx.lastToolCall.Name, "neither configured recovery-cap nor finalizer tool calls may be scheduled")
}

func TestUnknownOutcomeWithRequiredCompletionFailsWithoutMoreWork(t *testing.T) {
	rt := New()
	wfCtx := &testWorkflowContext{ctx: context.Background()}
	input := &RunInput{Policy: &PolicyOverrides{CompletionTool: "svc.persist"}}
	unknown := planner.NewToolErrorWithKind("execution outcome is unknown", planner.ToolErrorKindOutcomeUnknown)
	result := &planner.ToolResult{Name: "svc.effect", Error: unknown}

	out, err := rt.finalizeWithPlanner(
		wfCtx, AgentRegistration{}, input, &planner.PlanInput{}, []*planner.ToolResult{result}, nil,
		model.TokenUsage{}, policy.CapsState{}, 1, "turn-1", nil,
		planner.TerminationReasonFailureCap, time.Time{},
	)

	require.ErrorContains(t, err, "outcome_unknown")
	require.ErrorContains(t, err, "completion tool")
	require.Nil(t, out)
	require.Empty(t, wfCtx.plannerCalls)
	require.Empty(t, wfCtx.lastToolCall.Name)
}
