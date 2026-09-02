package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

type activityRecoveryUsagePlanner struct {
	streamReject bool
}

type activityRecoveryUsageClient struct {
	rejected     *model.OutputValidationError
	streamReject bool
}

type historyRecoveryCapturePlanner struct {
	startMessages  []*model.Message
	resumeMessages []*model.Message
}

type caughtRecoveryUsagePlanner struct {
	returnSecondRejection bool
}

func TestRecoveryTurnsConsumeOneReplacementPlannerActivity(t *testing.T) {
	caps := policy.CapsState{MaxRecoveryTurns: 2, RemainingRecoveryTurns: 2}

	require.True(t, consumeRecoveryTurn(&caps))
	require.Equal(t, 1, caps.RemainingRecoveryTurns)
	require.True(t, consumeRecoveryTurn(&caps))
	require.Zero(t, caps.RemainingRecoveryTurns)
	require.False(t, consumeRecoveryTurn(&caps))

	invalid := policy.CapsState{MaxRecoveryTurns: -1, RemainingRecoveryTurns: -1}
	require.False(t, consumeRecoveryTurn(&invalid))
	require.Equal(t, -1, invalid.RemainingRecoveryTurns)
	require.Zero(t, initialCaps(RunPolicy{MaxRecoveryTurns: -1}).RemainingRecoveryTurns)
}

func TestRecoveryTurnsResetOnlyAfterSuccessfulBudgetedDomainWork(t *testing.T) {
	rt := &Runtime{toolSpecs: map[tools.Ident]tools.ToolSpec{
		"svc.read": {Name: "svc.read", Toolset: "svc"},
	}}
	caps := policy.CapsState{MaxRecoveryTurns: 2, RemainingRecoveryTurns: 1}

	resetRecoveryTurnsAfterResults(rt, &caps, []*planner.ToolResult{{
		Name:  tools.ToolUnavailable,
		Error: planner.NewToolError("rejected"),
	}})
	require.Equal(t, 1, caps.RemainingRecoveryTurns)

	resetRecoveryTurnsAfterResults(rt, &caps, []*planner.ToolResult{{
		Name:  "svc.read",
		Error: planner.NewToolError("failed"),
	}})
	require.Equal(t, 1, caps.RemainingRecoveryTurns)

	resetRecoveryTurnsAfterResults(rt, &caps, []*planner.ToolResult{{Name: "svc.read"}})
	require.Equal(t, 2, caps.RemainingRecoveryTurns)

	caps.RemainingRecoveryTurns = 0
	mixed := []*planner.ToolResult{
		{Name: "svc.read"},
		{
			Name:  "svc.write",
			Error: planner.NewToolError("invalid arguments"),
			RetryHint: &planner.RetryHint{
				Reason: planner.RetryReasonInvalidArguments,
				Tool:   "svc.write",
			},
		},
	}
	resetRecoveryTurnsAfterResults(rt, &caps, mixed)
	require.True(t, resultsRequireRecovery(mixed))
	require.True(t, consumeRecoveryTurn(&caps))
	require.Equal(t, 1, caps.RemainingRecoveryTurns)
}

func TestMixedToolBatchStartsFreshRecoveryAllowanceInBothWorkflowPaths(t *testing.T) {
	t.Parallel()

	rt := New()
	rt.toolSpecs["svc.read"] = tools.ToolSpec{Name: "svc.read", Toolset: "svc"}
	rt.toolSpecs["svc.write"] = tools.ToolSpec{Name: "svc.write", Toolset: "svc"}
	results := []*planner.ToolResult{
		{Name: "svc.read"},
		{
			Name:  "svc.write",
			Error: planner.NewToolError("invalid arguments"),
			RetryHint: &planner.RetryHint{
				Reason: planner.RetryReasonInvalidArguments,
				Tool:   "svc.write",
			},
		},
	}
	newState := func() *runLoopState {
		return &runLoopState{
			Caps:        policy.CapsState{MaxRecoveryTurns: 2, RemainingRecoveryTurns: 0},
			NextAttempt: 2,
		}
	}
	input := &RunInput{AgentID: "svc.agent", RunID: "run-mixed-recovery"}
	base := &planner.PlanInput{RunContext: run.Context{RunID: input.RunID}}
	deadlines := &runDeadlines{}
	wfCtx := &testWorkflowContext{ctx: context.Background()}
	reg := AgentRegistration{ID: input.AgentID}

	st := newState()
	out, err := rt.applyFailureAndProtectionPolicy(wfCtx, reg, input, base, st, "turn-1", nil, deadlines, results)
	require.NoError(t, err)
	require.Nil(t, out)
	require.Equal(t, 1, st.Caps.RemainingRecoveryTurns)

	st = newState()
	out, err = rt.applyAwaitFailureCap(wfCtx, reg, input, base, st, results, "turn-1", deadlines)
	require.NoError(t, err)
	require.Nil(t, out)
	require.Equal(t, 1, st.Caps.RemainingRecoveryTurns)
}

func TestPlannerRecoveryCatalogMatchesModelVisibleInternalTool(t *testing.T) {
	t.Parallel()

	input := api.PlanActivityInput{
		ToolPolicyActive: true,
		AllowedTools:     []tools.Ident{"svc.read"},
	}
	require.NoError(t, validatePlannerRecoveryCatalog(&planner.PlanResult{
		ToolCalls: []planner.ToolRequest{{Name: tools.ToolUnavailable}},
	}, input))

	input.AllowedTools = nil
	require.Error(t, validatePlannerRecoveryCatalog(&planner.PlanResult{
		ToolCalls: []planner.ToolRequest{{Name: tools.ToolUnavailable}},
	}, input))
}

func TestPlannerRecoveryCatalogUsesRejectedRequestSnapshot(t *testing.T) {
	t.Parallel()

	input := api.PlanActivityInput{
		ToolPolicyActive: true,
		AllowedTools:     []tools.Ident{"svc.read", "svc.write"},
		Recovery: &api.ModelRecovery{
			ToolCatalog: []tools.Ident{"svc.read", tools.ToolUnavailable},
		},
	}
	require.NoError(t, validatePlannerRecoveryCatalog(&planner.PlanResult{
		ToolCalls: []planner.ToolRequest{{Name: "svc.read"}},
	}, input))
	require.Error(t, validatePlannerRecoveryCatalog(&planner.PlanResult{
		ToolCalls: []planner.ToolRequest{{Name: "svc.write"}},
	}, input))
}

func TestModelRecoveryCorrectionKeepsValidUTF8AtByteLimit(t *testing.T) {
	t.Parallel()

	rejected, err := model.RestoreOutputValidationError(
		model.OutputValidationToolIdentity,
		errors.New("private invalid output"),
		model.ResponseEvidence{Present: true, ByteCount: 64, Fingerprint: [32]byte{1}},
		nil,
	)
	require.NoError(t, err)
	request := &model.Request{Tools: []*model.ToolDefinition{{
		Name: strings.Repeat("界", maxRecoveryCorrectionBytes),
	}}}
	recorder := &modelRecoveryRecorder{}
	recorder.record(request, rejected)
	recovery, recoveryErr := recorder.recovery(rejected, 1)
	require.NoError(t, recoveryErr)
	require.NotNil(t, recovery)
	require.LessOrEqual(t, len(recovery.Correction), maxRecoveryCorrectionBytes)
	require.True(t, utf8.ValidString(recovery.Correction))
}

func TestRunPlanActivityRecoveringCarriesBoundedRecovery(t *testing.T) {
	t.Parallel()

	wfCtx := &testWorkflowContext{
		ctx: context.Background(),
		plannerOutputs: []*api.PlanActivityOutput{
			{
				Recovery: &api.ModelRecovery{
					Kind:         model.OutputValidationOutputBounds,
					ByteCount:    8192,
					Attempt:      1,
					Correction:   "replace with a shorter answer",
					DisableTools: true,
				},
				Usage: model.TokenUsage{InputTokens: 2, OutputTokens: 5},
			},
			{
				Result: &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
					Role:  model.ConversationRoleAssistant,
					Parts: []model.Part{model.TextPart{Text: "short answer"}},
				}}},
				Usage: model.TokenUsage{InputTokens: 3, OutputTokens: 4},
			},
		},
	}
	rt := &Runtime{logger: telemetry.NoopLogger{}}
	caps := policy.CapsState{MaxRecoveryTurns: 1, RemainingRecoveryTurns: 1}
	nextAttempt := 2

	output, err := rt.runPlanActivityRecovering(
		wfCtx,
		"resume",
		engine.ActivityOptions{},
		api.PlanActivityInput{
			AgentID:          "svc.agent",
			RunID:            "run-1",
			RunContext:       run.Context{RunID: "run-1", Attempt: 1},
			ToolPolicyActive: true,
			AllowedTools:     []tools.Ident{"svc.read"},
			PolicyCaps:       caps,
		},
		time.Time{},
		&caps,
		&nextAttempt,
	)
	require.NoError(t, err)
	require.Equal(t, model.TokenUsage{InputTokens: 5, OutputTokens: 9}, output.Usage)
	require.Zero(t, caps.RemainingRecoveryTurns)
	require.Equal(t, 3, nextAttempt)
	require.Len(t, wfCtx.plannerCalls, 2)
	require.Nil(t, wfCtx.plannerCalls[0].Input.Recovery)
	require.Equal(t, 1, wfCtx.plannerCalls[0].Input.RunContext.Attempt)
	require.Equal(t, "replace with a shorter answer", wfCtx.plannerCalls[1].Input.Recovery.Correction)
	require.Equal(t, 2, wfCtx.plannerCalls[1].Input.RunContext.Attempt)
	require.True(t, wfCtx.plannerCalls[1].Input.ToolPolicyActive)
	require.Empty(t, wfCtx.plannerCalls[1].Input.AllowedTools)
}

func TestRunPlanActivityRecoveringReturnsLastRecoveryAtCap(t *testing.T) {
	t.Parallel()

	wfCtx := &testWorkflowContext{
		ctx: context.Background(),
		plannerOutputs: []*api.PlanActivityOutput{
			{
				Recovery: &api.ModelRecovery{
					Kind:        model.OutputValidationToolArguments,
					ByteCount:   10,
					Attempt:     1,
					Correction:  "replace invalid arguments",
					ToolCatalog: []tools.Ident{},
				},
				Usage: model.TokenUsage{InputTokens: 2, OutputTokens: 1},
			},
			{
				Recovery: &api.ModelRecovery{
					Kind:        model.OutputValidationToolArguments,
					ByteCount:   11,
					Attempt:     2,
					Correction:  "replace invalid arguments again",
					ToolCatalog: []tools.Ident{},
				},
				Usage: model.TokenUsage{InputTokens: 3, OutputTokens: 2},
			},
		},
	}
	rt := New()
	caps := policy.CapsState{MaxRecoveryTurns: 1, RemainingRecoveryTurns: 1}
	nextAttempt := 2

	out, err := rt.runPlanActivityRecovering(
		wfCtx,
		"plan",
		engine.ActivityOptions{},
		api.PlanActivityInput{
			AgentID:    "svc.agent",
			RunID:      "run-cap",
			RunContext: run.Context{RunID: "run-cap", Attempt: 1},
			PolicyCaps: caps,
		},
		time.Time{},
		&caps,
		&nextAttempt,
	)
	require.ErrorIs(t, err, errRecoveryTurnCapExceeded)
	require.NotNil(t, out)
	require.NotNil(t, out.Recovery)
	require.Equal(t, model.TokenUsage{InputTokens: 5, OutputTokens: 3}, out.Usage)
	require.Zero(t, caps.RemainingRecoveryTurns)
	require.Equal(t, 3, nextAttempt)
	require.Len(t, wfCtx.plannerCalls, 2)
}

func TestRunPlanActivityRecoveringRejectsUsageOverflow(t *testing.T) {
	t.Parallel()

	wfCtx := &testWorkflowContext{
		ctx: context.Background(),
		plannerOutputs: []*api.PlanActivityOutput{
			{
				Recovery: &api.ModelRecovery{
					Kind:        model.OutputValidationToolArguments,
					ByteCount:   10,
					Attempt:     1,
					Correction:  "replace invalid arguments",
					ToolCatalog: []tools.Ident{},
				},
				Usage: model.TokenUsage{InputTokens: math.MaxInt},
			},
			{
				Result: &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
					Role:  model.ConversationRoleAssistant,
					Parts: []model.Part{model.TextPart{Text: "done"}},
				}}},
				Usage: model.TokenUsage{InputTokens: 1},
			},
		},
	}
	rt := New()
	caps := policy.CapsState{MaxRecoveryTurns: 1, RemainingRecoveryTurns: 1}

	out, err := rt.runPlanActivityRecovering(
		wfCtx,
		"plan",
		engine.ActivityOptions{},
		api.PlanActivityInput{RunContext: run.Context{Attempt: 1}, PolicyCaps: caps},
		time.Time{},
		&caps,
		nil,
	)
	require.Nil(t, out)
	require.ErrorContains(t, err, "token usage aggregation overflows")
	require.Len(t, wfCtx.plannerCalls, 2)
}

func TestExecuteWorkflowRunFinalizesAfterInitialModelRecoveryCap(t *testing.T) {
	t.Parallel()

	wfCtx := &testWorkflowContext{
		ctx: context.Background(),
		plannerOutputs: []*api.PlanActivityOutput{
			{
				Recovery: &api.ModelRecovery{
					Kind:        model.OutputValidationToolArguments,
					ByteCount:   10,
					Attempt:     1,
					Correction:  "replace invalid arguments",
					ToolCatalog: []tools.Ident{},
				},
				Usage: model.TokenUsage{InputTokens: 2, OutputTokens: 1},
			},
			{
				Recovery: &api.ModelRecovery{
					Kind:        model.OutputValidationToolArguments,
					ByteCount:   11,
					Attempt:     2,
					Correction:  "replace invalid arguments again",
					ToolCatalog: []tools.Ident{},
				},
				Usage: model.TokenUsage{InputTokens: 3, OutputTokens: 2},
			},
			{
				Result: &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
					Role:  model.ConversationRoleAssistant,
					Parts: []model.Part{model.TextPart{Text: "best available answer"}},
				}}},
				Usage: model.TokenUsage{InputTokens: 4, OutputTokens: 3},
			},
		},
	}
	rt := New()
	input := &RunInput{AgentID: "svc.agent", RunID: "run-initial-cap"}
	reg := AgentRegistration{
		ID:                 input.AgentID,
		PlanActivityName:   "plan",
		ResumeActivityName: "resume",
		Policy:             RunPolicy{MaxRecoveryTurns: 1},
	}

	out, status, err := rt.executeWorkflowRun(
		wfCtx,
		reg,
		input,
		run.Context{RunID: input.RunID, Attempt: 1},
		"turn-1",
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, runStatusSuccess, status)
	require.Equal(t, "best available answer", agentMessageText(out.Final))
	require.Equal(t, &model.TokenUsage{InputTokens: 9, OutputTokens: 6}, out.Usage)
	require.Len(t, wfCtx.plannerCalls, 3)
	require.Equal(t, planner.TerminationReasonFailureCap, wfCtx.plannerCalls[2].Input.Finalize.Reason)
	hint := wfCtx.plannerCalls[2].Input.Finalize.Message
	require.Contains(t, hint, "recovery budget exhausted")
	require.NotContains(t, hint, "tool failures")
	require.NotContains(t, hint, "invalid arguments")
}

func TestResumeAfterToolTurnFinalizesAfterModelRecoveryCap(t *testing.T) {
	t.Parallel()

	wfCtx := &testWorkflowContext{
		ctx: context.Background(),
		plannerOutputs: []*api.PlanActivityOutput{
			{
				Recovery: &api.ModelRecovery{
					Kind:        model.OutputValidationToolArguments,
					ByteCount:   10,
					Attempt:     2,
					Correction:  "replace invalid arguments",
					ToolCatalog: []tools.Ident{},
				},
				Usage: model.TokenUsage{InputTokens: 2, OutputTokens: 1},
			},
			{
				Recovery: &api.ModelRecovery{
					Kind:        model.OutputValidationToolArguments,
					ByteCount:   11,
					Attempt:     3,
					Correction:  "replace invalid arguments again",
					ToolCatalog: []tools.Ident{},
				},
				Usage: model.TokenUsage{InputTokens: 3, OutputTokens: 2},
			},
			{
				Result: &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
					Role:  model.ConversationRoleAssistant,
					Parts: []model.Part{model.TextPart{Text: "resumed best answer"}},
				}}},
				Usage: model.TokenUsage{InputTokens: 4, OutputTokens: 3},
			},
		},
	}
	rt := New()
	input := &RunInput{AgentID: "svc.agent", RunID: "run-resume-cap"}
	base := &planner.PlanInput{RunContext: run.Context{RunID: input.RunID, Attempt: 1}}
	st := &runLoopState{
		Caps:        policy.CapsState{MaxRecoveryTurns: 1, RemainingRecoveryTurns: 1},
		NextAttempt: 2,
		AggUsage:    model.TokenUsage{InputTokens: 1, OutputTokens: 1},
	}
	reg := AgentRegistration{ID: input.AgentID, ResumeActivityName: "resume"}
	deadlines := &runDeadlines{}

	out, err := rt.resumeAfterToolTurn(wfCtx, reg, input, base, st, engine.ActivityOptions{}, deadlines, "turn-1")
	require.NoError(t, err)
	require.Equal(t, "resumed best answer", agentMessageText(out.Final))
	require.Equal(t, &model.TokenUsage{InputTokens: 10, OutputTokens: 7}, out.Usage)
	require.Len(t, wfCtx.plannerCalls, 3)
	require.Equal(t, planner.TerminationReasonFailureCap, wfCtx.plannerCalls[2].Input.Finalize.Reason)
}

func TestRunPlanActivityRecoveringUsesExactRejectedRequestCatalog(t *testing.T) {
	t.Parallel()

	wfCtx := &testWorkflowContext{
		ctx: context.Background(),
		plannerOutputs: []*api.PlanActivityOutput{{
			Recovery: &api.ModelRecovery{
				Kind:        model.OutputValidationToolArguments,
				Correction:  "replace the invalid arguments",
				ToolCatalog: []tools.Ident{"svc.read", tools.ToolUnavailable},
			},
		}, {
			Result: &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
				Role:  model.ConversationRoleAssistant,
				Parts: []model.Part{model.TextPart{Text: "done"}},
			}}},
		}},
	}
	rt := &Runtime{logger: telemetry.NoopLogger{}}
	caps := policy.CapsState{MaxRecoveryTurns: 1, RemainingRecoveryTurns: 1}

	_, err := rt.runPlanActivityRecovering(
		wfCtx,
		"resume",
		engine.ActivityOptions{},
		api.PlanActivityInput{
			AgentID:          "svc.agent",
			RunID:            "run-exact-catalog",
			RunContext:       run.Context{RunID: "run-exact-catalog"},
			ToolPolicyActive: true,
			AllowedTools:     []tools.Ident{"svc.read", "svc.write"},
			PolicyCaps:       caps,
		},
		time.Time{},
		&caps,
		nil,
	)
	require.NoError(t, err)
	require.Len(t, wfCtx.plannerCalls, 2)
	require.Equal(t, []tools.Ident{"svc.read", tools.ToolUnavailable}, wfCtx.plannerCalls[1].Input.AllowedTools)
}

func TestValidatePlanActivityOutputRejectsInvalidRecovery(t *testing.T) {
	t.Parallel()

	input := api.PlanActivityInput{
		RunContext:       run.Context{Attempt: 4},
		ToolPolicyActive: true,
		AllowedTools:     []tools.Ident{"svc.read"},
	}
	valid := func() *api.ModelRecovery {
		return &api.ModelRecovery{
			Kind:        model.OutputValidationToolArguments,
			ByteCount:   12,
			Attempt:     4,
			Correction:  "replace the invalid arguments",
			ToolCatalog: []tools.Ident{"svc.read", tools.ToolUnavailable},
		}
	}
	require.NoError(t, validatePlanActivityOutput(&api.PlanActivityOutput{Recovery: valid()}, input))

	tests := []struct {
		name   string
		mutate func(*api.ModelRecovery)
	}{
		{name: "blank correction", mutate: func(recovery *api.ModelRecovery) { recovery.Correction = "  " }},
		{name: "oversized correction", mutate: func(recovery *api.ModelRecovery) {
			recovery.Correction = strings.Repeat("x", maxRecoveryCorrectionBytes+1)
		}},
		{name: "invalid utf8", mutate: func(recovery *api.ModelRecovery) { recovery.Correction = string([]byte{0xff}) }},
		{name: "negative byte count", mutate: func(recovery *api.ModelRecovery) { recovery.ByteCount = -1 }},
		{name: "attempt mismatch", mutate: func(recovery *api.ModelRecovery) { recovery.Attempt = 3 }},
		{name: "invalid usage", mutate: func(recovery *api.ModelRecovery) {
			recovery.Usage = model.TokenUsage{InputTokens: -1}
		}},
		{name: "unsupported kind", mutate: func(recovery *api.ModelRecovery) {
			recovery.Kind = model.OutputValidationUsage
		}},
		{name: "tool recovery disables tools", mutate: func(recovery *api.ModelRecovery) { recovery.DisableTools = true }},
		{name: "nil tool catalog", mutate: func(recovery *api.ModelRecovery) { recovery.ToolCatalog = nil }},
		{name: "widened tool catalog", mutate: func(recovery *api.ModelRecovery) {
			recovery.ToolCatalog = append(recovery.ToolCatalog, "svc.unadvertised")
		}},
		{name: "duplicate tool catalog", mutate: func(recovery *api.ModelRecovery) {
			recovery.ToolCatalog = append(recovery.ToolCatalog, "svc.read")
		}},
		{name: "oversized tool catalog", mutate: func(recovery *api.ModelRecovery) {
			recovery.ToolCatalog = make([]tools.Ident, maxRecoveryToolCatalogEntries+1)
			for i := range recovery.ToolCatalog {
				recovery.ToolCatalog[i] = tools.Ident(fmt.Sprintf("svc.tool_%d", i))
			}
		}},
		{name: "final recovery keeps tools", mutate: func(recovery *api.ModelRecovery) {
			recovery.Kind = model.OutputValidationOutputBounds
		}},
		{name: "final recovery persists tool catalog", mutate: func(recovery *api.ModelRecovery) {
			recovery.Kind = model.OutputValidationOutputBounds
			recovery.DisableTools = true
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recovery := valid()
			test.mutate(recovery)
			err := validatePlanActivityOutput(&api.PlanActivityOutput{Recovery: recovery}, input)
			require.ErrorContains(t, err, "invalid Recovery")
		})
	}
}

func TestValidatePlanActivityOutputRejectsInvalidTopLevelUsage(t *testing.T) {
	t.Parallel()

	input := api.PlanActivityInput{RunContext: run.Context{Attempt: 4}}
	tests := []struct {
		name string
		out  *api.PlanActivityOutput
	}{
		{
			name: "result",
			out: &api.PlanActivityOutput{
				Result: &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
					Role:  model.ConversationRoleAssistant,
					Parts: []model.Part{model.TextPart{Text: "done"}},
				}}},
				Usage: model.TokenUsage{InputTokens: -1},
			},
		},
		{
			name: "recovery",
			out: &api.PlanActivityOutput{
				Recovery: &api.ModelRecovery{
					Kind:       model.OutputValidationToolArguments,
					ByteCount:  12,
					Attempt:    4,
					Correction: "replace the invalid arguments",
				},
				Usage: model.TokenUsage{OutputTokens: -1},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := validatePlanActivityOutput(test.out, input)
			require.ErrorContains(t, err, "invalid Usage")
		})
	}
}

func TestRunPlanActivityNormalizesCompatibilityEchoes(t *testing.T) {
	t.Parallel()

	input := api.PlanActivityInput{
		AgentID:          "svc.agent",
		RunID:            "run-owned-state",
		RunContext:       run.Context{RunID: "run-owned-state", Attempt: 4},
		ToolPolicyActive: true,
		AllowedTools:     []tools.Ident{"svc.allowed"},
		PolicyCaps: policy.CapsState{
			MaxToolCalls:           5,
			RemainingToolCalls:     3,
			MaxRecoveryTurns:       2,
			RemainingRecoveryTurns: 1,
		},
	}
	wfCtx := &testWorkflowContext{
		ctx: context.Background(),
		plannerOutputs: []*api.PlanActivityOutput{{
			Result: &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
				Role:  model.ConversationRoleAssistant,
				Parts: []model.Part{model.TextPart{Text: "done"}},
			}}},
			ToolPolicyActive: false,
			AllowedTools:     []tools.Ident{"svc.injected"},
			PolicyCaps:       policy.CapsState{MaxRecoveryTurns: 99, RemainingRecoveryTurns: 99},
			Attempt:          99,
		}},
	}
	rt := &Runtime{logger: telemetry.NoopLogger{}}

	out, err := rt.runPlanActivity(wfCtx, "plan", engine.ActivityOptions{}, input, time.Time{})
	require.NoError(t, err)
	require.Equal(t, input.ToolPolicyActive, out.ToolPolicyActive)
	require.Equal(t, input.AllowedTools, out.AllowedTools)
	require.Equal(t, input.PolicyCaps, out.PolicyCaps)
	require.Equal(t, input.RunContext.Attempt, out.Attempt)
}

func TestRunPlanActivityRecoveringRechecksInputBudgetAfterRecovery(t *testing.T) {
	t.Parallel()

	input := api.PlanActivityInput{
		AgentID:    "svc.agent",
		RunID:      "run-recovery-budget",
		RunContext: run.Context{RunID: "run-recovery-budget", Attempt: 1},
		Messages: []*model.Message{{
			Role: model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{
				Text: strings.Repeat("x", maxPlanActivityInputBytes-3_000),
			}},
		}},
	}
	require.NoError(t, enforcePlanActivityInputBudget(input))
	wfCtx := &testWorkflowContext{
		ctx: context.Background(),
		plannerOutputs: []*api.PlanActivityOutput{{
			Recovery: &api.ModelRecovery{
				Kind:        model.OutputValidationToolArguments,
				ByteCount:   10,
				Attempt:     1,
				Correction:  strings.Repeat("c", maxRecoveryCorrectionBytes),
				ToolCatalog: []tools.Ident{},
			},
		}},
	}
	rt := &Runtime{logger: telemetry.NoopLogger{}}
	caps := policy.CapsState{MaxRecoveryTurns: 1, RemainingRecoveryTurns: 1}

	out, err := rt.runPlanActivityRecovering(
		wfCtx,
		"resume",
		engine.ActivityOptions{},
		input,
		time.Time{},
		&caps,
		nil,
	)
	require.Nil(t, out)
	require.ErrorContains(t, err, "plan activity input exceeds budget")
	require.Len(t, wfCtx.plannerCalls, 1)
}

func TestPlanActivitiesPreserveUsageAcrossModelRecovery(t *testing.T) {
	rejectedUsage := model.TokenUsage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}
	rejected, err := model.RestoreOutputValidationError(
		model.OutputValidationToolArguments,
		errors.New("private invalid arguments"),
		model.ResponseEvidence{Present: true, ByteCount: 21},
		&rejectedUsage,
	)
	require.NoError(t, err)
	wantUsage := model.TokenUsage{InputTokens: 13, OutputTokens: 6, TotalTokens: 19}

	tests := []struct {
		name         string
		streamReject bool
		activity     func(*Runtime, context.Context, *PlanActivityInput) (*PlanActivityOutput, error)
	}{
		{name: "start unary rejection", activity: func(rt *Runtime, ctx context.Context, input *PlanActivityInput) (*PlanActivityOutput, error) {
			return rt.PlanStartActivity(ctx, input)
		}},
		{name: "resume unary rejection", activity: func(rt *Runtime, ctx context.Context, input *PlanActivityInput) (*PlanActivityOutput, error) {
			return rt.PlanResumeActivity(ctx, input)
		}},
		{name: "start stream rejection", streamReject: true, activity: func(rt *Runtime, ctx context.Context, input *PlanActivityInput) (*PlanActivityOutput, error) {
			return rt.PlanStartActivity(ctx, input)
		}},
		{name: "resume stream rejection", streamReject: true, activity: func(rt *Runtime, ctx context.Context, input *PlanActivityInput) (*PlanActivityOutput, error) {
			return rt.PlanResumeActivity(ctx, input)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rt := New()
			rt.models["default"] = &activityRecoveryUsageClient{rejected: rejected, streamReject: test.streamReject}
			rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: activityRecoveryUsagePlanner{streamReject: test.streamReject}}
			input := &PlanActivityInput{
				AgentID:    "svc.agent",
				RunID:      "run-recovery-usage",
				RunContext: run.Context{RunID: "run-recovery-usage", Attempt: 1},
			}

			out, activityErr := test.activity(rt, context.Background(), input)
			require.NoError(t, activityErr)
			require.NotNil(t, out.Recovery)
			require.Equal(t, rejectedUsage, out.Recovery.Usage)
			require.Equal(t, wantUsage, out.Usage)
		})
	}
}

func TestPlanActivitiesAccountCaughtUnaryRejections(t *testing.T) {
	rejectedUsage := model.TokenUsage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}
	rejected, err := model.RestoreOutputValidationError(
		model.OutputValidationToolArguments,
		errors.New("private invalid arguments"),
		model.ResponseEvidence{Present: true, ByteCount: 21},
		&rejectedUsage,
	)
	require.NoError(t, err)

	activities := []struct {
		name string
		run  func(*Runtime, context.Context, *PlanActivityInput) (*PlanActivityOutput, error)
	}{
		{name: "start", run: func(rt *Runtime, ctx context.Context, input *PlanActivityInput) (*PlanActivityOutput, error) {
			return rt.PlanStartActivity(ctx, input)
		}},
		{name: "resume", run: func(rt *Runtime, ctx context.Context, input *PlanActivityInput) (*PlanActivityOutput, error) {
			return rt.PlanResumeActivity(ctx, input)
		}},
	}
	for _, activity := range activities {
		for _, secondRejected := range []bool{false, true} {
			name := "then success"
			if secondRejected {
				name = "then selected rejection"
			}
			t.Run(activity.name+" "+name, func(t *testing.T) {
				rt := New()
				rt.models["default"] = &activityRecoveryUsageClient{rejected: rejected}
				rt.agents["svc.agent"] = AgentRegistration{
					ID:      "svc.agent",
					Planner: caughtRecoveryUsagePlanner{returnSecondRejection: secondRejected},
				}
				out, activityErr := activity.run(rt, context.Background(), &PlanActivityInput{
					AgentID:    "svc.agent",
					RunID:      "run-caught-rejection",
					RunContext: run.Context{RunID: "run-caught-rejection", Attempt: 1},
				})
				require.NoError(t, activityErr)
				if secondRejected {
					require.NotNil(t, out.Recovery)
					require.Equal(t, model.TokenUsage{InputTokens: 6, OutputTokens: 4, TotalTokens: 10}, out.Usage)
					return
				}
				require.NotNil(t, out.Result)
				require.Equal(t, model.TokenUsage{InputTokens: 13, OutputTokens: 6, TotalTokens: 19}, out.Usage)
			})
		}
	}
}

func TestPlanActivitiesDoNotRecoverHookPersistenceFailure(t *testing.T) {
	rejected, err := model.RestoreOutputValidationError(
		model.OutputValidationToolArguments,
		errors.New("private invalid arguments"),
		model.ResponseEvidence{Present: true, ByteCount: 21},
		nil,
	)
	require.NoError(t, err)
	hookErr := errors.New("persist planner event")

	tests := []struct {
		name     string
		activity func(*Runtime, context.Context, *PlanActivityInput) (*PlanActivityOutput, error)
	}{
		{name: "start", activity: func(rt *Runtime, ctx context.Context, input *PlanActivityInput) (*PlanActivityOutput, error) {
			return rt.PlanStartActivity(ctx, input)
		}},
		{name: "resume", activity: func(rt *Runtime, ctx context.Context, input *PlanActivityInput) (*PlanActivityOutput, error) {
			return rt.PlanResumeActivity(ctx, input)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rt := New(WithRunEventStore(&recordingRunlog{err: hookErr}))
			rt.models["default"] = &activityRecoveryUsageClient{rejected: rejected}
			rt.agents["svc.agent"] = AgentRegistration{ID: "svc.agent", Planner: activityRecoveryUsagePlanner{}}
			input := &PlanActivityInput{
				AgentID:    "svc.agent",
				RunID:      "run-hook-failure",
				RunContext: run.Context{RunID: "run-hook-failure", Attempt: 1},
			}

			out, activityErr := test.activity(rt, context.Background(), input)
			require.Nil(t, out)
			require.ErrorIs(t, activityErr, rejected)
			require.ErrorIs(t, activityErr, hookErr)
		})
	}
}

func TestPlanActivitiesAppendRecoveryAfterHistoryPolicy(t *testing.T) {
	tests := []struct {
		name     string
		activity func(*Runtime, context.Context, *PlanActivityInput) (*PlanActivityOutput, error)
		messages func(*historyRecoveryCapturePlanner) []*model.Message
	}{
		{
			name: "start",
			activity: func(rt *Runtime, ctx context.Context, input *PlanActivityInput) (*PlanActivityOutput, error) {
				return rt.PlanStartActivity(ctx, input)
			},
			messages: func(planner *historyRecoveryCapturePlanner) []*model.Message { return planner.startMessages },
		},
		{
			name: "resume",
			activity: func(rt *Runtime, ctx context.Context, input *PlanActivityInput) (*PlanActivityOutput, error) {
				return rt.PlanResumeActivity(ctx, input)
			},
			messages: func(planner *historyRecoveryCapturePlanner) []*model.Message { return planner.resumeMessages },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capture := &historyRecoveryCapturePlanner{}
			rt := New()
			rt.agents["svc.agent"] = AgentRegistration{
				ID:      "svc.agent",
				Planner: capture,
				Policy: RunPolicy{History: func(context.Context, []*model.Message, []*model.ToolDefinition) ([]*model.Message, error) {
					return []*model.Message{{
						Role:  model.ConversationRoleUser,
						Parts: []model.Part{model.TextPart{Text: "fresh history slice"}},
					}}, nil
				}},
			}
			input := &PlanActivityInput{
				AgentID:    "svc.agent",
				RunID:      "run-history-recovery",
				RunContext: run.Context{RunID: "run-history-recovery", Attempt: 2},
				Messages: []*model.Message{{
					Role:  model.ConversationRoleUser,
					Parts: []model.Part{model.TextPart{Text: "original history"}},
				}},
				Recovery: &api.ModelRecovery{
					Kind:       model.OutputValidationToolArguments,
					Attempt:    1,
					Correction: "use the safe field contract",
				},
			}

			out, activityErr := test.activity(rt, context.Background(), input)
			require.NoError(t, activityErr)
			require.NotNil(t, out.Result)
			messages := test.messages(capture)
			require.Len(t, messages, 2)
			require.Equal(t, "fresh history slice", messages[0].Parts[0].(model.TextPart).Text)
			require.Equal(t, model.ConversationRoleSystem, messages[1].Role)
			require.Equal(t, "use the safe field contract", messages[1].Parts[0].(model.TextPart).Text)
		})
	}
}

func TestRunFinalizationPlanRecoversRejectedFinalAnswer(t *testing.T) {
	t.Parallel()

	wfCtx := &testWorkflowContext{
		ctx: context.Background(),
		plannerOutputs: []*api.PlanActivityOutput{
			{
				Recovery: &api.ModelRecovery{
					Kind:         model.OutputValidationStructuredOutput,
					ByteCount:    256,
					Attempt:      4,
					Correction:   "replace with schema-valid output",
					DisableTools: true,
				},
				Usage: model.TokenUsage{InputTokens: 1, OutputTokens: 2},
			},
			{
				Result: &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
					Role:  model.ConversationRoleAssistant,
					Parts: []model.Part{model.TextPart{Text: `{"status":"done"}`}},
				}}},
				Usage: model.TokenUsage{InputTokens: 3, OutputTokens: 4},
			},
		},
	}
	rt := &Runtime{logger: telemetry.NoopLogger{}}
	base := &planner.PlanInput{
		Messages:   []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "finish"}}}},
		RunContext: run.Context{RunID: "run-finalize", Attempt: 4},
	}

	output, usage, err := rt.runFinalizationPlan(
		wfCtx,
		AgentRegistration{ResumeActivityName: "resume"},
		&RunInput{AgentID: "svc.agent", RunID: "run-finalize"},
		base,
		nil,
		model.TokenUsage{},
		policy.CapsState{MaxRecoveryTurns: 1, RemainingRecoveryTurns: 1},
		4,
		"turn-1",
		planner.TerminationReasonToolCap,
		time.Time{},
	)
	require.NoError(t, err)
	require.Equal(t, model.TokenUsage{InputTokens: 4, OutputTokens: 6}, usage)
	require.JSONEq(t, `{"status":"done"}`, agentMessageText(output.Result.FinalResponse.Message))
	require.Len(t, wfCtx.plannerCalls, 2)
	require.NotNil(t, wfCtx.plannerCalls[1].Input.Recovery)
	require.Empty(t, wfCtx.plannerCalls[1].Input.AllowedTools)
}

func TestRunFinalizationPlanStopsWhenToolFreeAnswerIsRejectedAtCap(t *testing.T) {
	t.Parallel()

	wfCtx := &testWorkflowContext{
		ctx: context.Background(),
		plannerOutputs: []*api.PlanActivityOutput{{
			Recovery: &api.ModelRecovery{
				Kind:         model.OutputValidationStructuredOutput,
				ByteCount:    128,
				Attempt:      4,
				Correction:   "replace with schema-valid output",
				DisableTools: true,
			},
			Usage: model.TokenUsage{InputTokens: 2, OutputTokens: 1},
		}},
	}
	rt := New()
	base := &planner.PlanInput{RunContext: run.Context{RunID: "run-finalizer-cap", Attempt: 3}}

	output, usage, err := rt.runFinalizationPlan(
		wfCtx,
		AgentRegistration{ResumeActivityName: "resume"},
		&RunInput{AgentID: "svc.agent", RunID: "run-finalizer-cap"},
		base,
		nil,
		model.TokenUsage{InputTokens: 1},
		policy.CapsState{MaxRecoveryTurns: 1, RemainingRecoveryTurns: 0},
		4,
		"turn-1",
		planner.TerminationReasonFailureCap,
		time.Time{},
	)
	require.Nil(t, output)
	require.Equal(t, model.TokenUsage{InputTokens: 1}, usage)
	require.ErrorIs(t, err, errRecoveryTurnCapExceeded)
	require.Len(t, wfCtx.plannerCalls, 1)
}

func (p activityRecoveryUsagePlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	return p.plan(ctx, input.Agent)
}

func (p caughtRecoveryUsagePlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	return p.plan(ctx, input.Agent)
}

func (p caughtRecoveryUsagePlanner) PlanResume(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return p.plan(ctx, input.Agent)
}

func (p caughtRecoveryUsagePlanner) plan(ctx context.Context, agentContext planner.PlannerContext) (*planner.PlanResult, error) {
	client, ok := agentContext.ModelClient("default")
	if !ok {
		return nil, errors.New("default model is not registered")
	}
	if _, err := client.Complete(ctx, &model.Request{Model: "rejected"}); err == nil {
		return nil, errors.New("expected first model output rejection")
	}
	if p.returnSecondRejection {
		_, err := client.Complete(ctx, &model.Request{Model: "rejected"})
		return nil, err
	}
	response, err := client.Complete(ctx, &model.Request{Model: "successful"})
	if err != nil {
		return nil, err
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &response.Content[0]}}, nil
}

func (p activityRecoveryUsagePlanner) PlanResume(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return p.plan(ctx, input.Agent)
}

func (p activityRecoveryUsagePlanner) plan(ctx context.Context, agentContext planner.PlannerContext) (*planner.PlanResult, error) {
	client, ok := agentContext.ModelClient("default")
	if !ok {
		return nil, errors.New("default model is not registered")
	}
	if _, err := client.Complete(ctx, &model.Request{Model: "successful"}); err != nil {
		return nil, err
	}
	if !p.streamReject {
		_, err := client.Complete(ctx, &model.Request{Model: "rejected"})
		return nil, err
	}
	stream, err := client.Stream(ctx, &model.Request{Model: "rejected", Stream: true})
	if err != nil {
		return nil, err
	}
	for {
		_, err = stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil, stream.Finalize(nil)
		}
		if err != nil {
			return nil, stream.Finalize(err)
		}
	}
}

func (c *activityRecoveryUsageClient) Complete(_ context.Context, request *model.Request) (*model.Response, error) {
	if request.Model == "rejected" {
		return nil, c.rejected
	}
	return &model.Response{
		Content: []model.Message{{
			Role:  model.ConversationRoleAssistant,
			Parts: []model.Part{model.TextPart{Text: "accepted"}},
		}},
		Usage:      model.TokenUsage{InputTokens: 10, OutputTokens: 4, TotalTokens: 14},
		StopReason: "end_turn",
	}, nil
}

func (c *activityRecoveryUsageClient) Stream(context.Context, *model.Request) (model.ValidatedStreamer, error) {
	if !c.streamReject {
		return nil, errors.New("unexpected stream")
	}
	return &modelTracingScriptedStreamer{
		chunks: []model.Chunk{
			model.UsageChunk{Usage: model.TokenUsage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}},
			model.StopChunk{Reason: "stop"},
		},
		finalizeErr: c.rejected,
	}, nil
}

func (p *historyRecoveryCapturePlanner) PlanStart(_ context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	p.startMessages = input.Messages
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
		Role:  model.ConversationRoleAssistant,
		Parts: []model.Part{model.TextPart{Text: "done"}},
	}}}, nil
}

func (p *historyRecoveryCapturePlanner) PlanResume(_ context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	p.resumeMessages = input.Messages
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
		Role:  model.ConversationRoleAssistant,
		Parts: []model.Part{model.TextPart{Text: "done"}},
	}}}, nil
}
