package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBookkeepingToolBatchAdmissionIsAtomic(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	for _, spec := range []tools.ToolSpec{
		bookkeepingSpec("control.progress", false),
		bookkeepingSpec("control.complete", true),
		newAnyJSONSpec("domain.lookup", "domain"),
		newAnyJSONSpec("domain.fetch", "domain"),
	} {
		rt.toolSpecs[spec.Name] = spec
	}

	tests := []struct {
		name     string
		calls    []planner.ToolRequest
		caps     policy.CapsState
		cost     int
		admitted bool
	}{
		{
			name: "bookkeeping survives exhausted domain budget",
			calls: []planner.ToolRequest{
				{Name: "control.progress"},
				{Name: "control.complete"},
			},
			caps:     policy.CapsState{MaxToolCalls: 2},
			admitted: true,
		},
		{
			name: "mixed over-budget response is rejected whole",
			calls: []planner.ToolRequest{
				{Name: "domain.lookup"},
				{Name: "control.progress"},
				{Name: "domain.fetch"},
			},
			caps: policy.CapsState{
				MaxToolCalls:       3,
				RemainingToolCalls: 1,
			},
			cost: 2,
		},
		{
			name: "unknown tools remain budgeted",
			calls: []planner.ToolRequest{
				{Name: "unknown"},
			},
			caps: policy.CapsState{
				MaxToolCalls: 1,
			},
			cost: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cost, admitted := rt.admitToolBatch(test.calls, test.caps)
			assert.Equal(t, test.cost, cost)
			assert.Equal(t, test.admitted, admitted)
		})
	}
}

func TestToolMetadataIncludesBudgetClass(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	bookkeeping := bookkeepingSpec("control.progress", false)
	budgeted := newAnyJSONSpec("domain.lookup", "domain")
	rt.toolSpecs[bookkeeping.Name] = bookkeeping
	rt.toolSpecs[budgeted.Name] = budgeted

	metadata := rt.toolMetadata([]planner.ToolRequest{
		{Name: bookkeeping.Name},
		{Name: budgeted.Name},
		{Name: "unknown"},
	})

	require.Len(t, metadata, 3)
	assert.Equal(t, policy.ToolBudgetClassBookkeeping, metadata[0].BudgetClass)
	assert.Equal(t, policy.ToolBudgetClassBudgeted, metadata[1].BudgetClass)
	assert.Equal(t, policy.ToolBudgetClassBudgeted, metadata[2].BudgetClass)
}

func TestSuccessfulBookkeepingStaysInTranscriptButNotPlannerContext(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	spec := bookkeepingSpec("control.progress", false)
	rt.toolSpecs[spec.Name] = spec
	call := planner.ToolRequest{Name: spec.Name, ToolCallID: "progress-1"}
	result := &planner.ToolResult{Name: spec.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"saved": true}}
	base := &planner.PlanInput{}
	ledger := transcript.NewLedger()

	rt.recordAssistantTurn(base, nil, []planner.ToolRequest{call}, ledger)
	rt.hideSuccessfulBookkeepingCallsFromPlanner(base, []*planner.ToolResult{result})
	require.NoError(t, rt.appendUserToolResults(base, []planner.ToolRequest{call}, []*planner.ToolResult{result}, ledger))

	require.Empty(t, base.Messages)
	require.Len(t, ledger.BuildMessages(), 2)
}

func TestConfirmedBookkeepingStaysInTranscriptButNotPlannerContext(t *testing.T) {
	for _, executed := range []bool{false, true} {
		name := "denied"
		if executed {
			name = "executed"
		}
		t.Run(name, func(t *testing.T) {
			rt := New(WithLogger(telemetry.NoopLogger{}))
			spec := bookkeepingSpec("control.progress", false)
			rt.toolSpecs[spec.Name] = spec
			call := planner.ToolRequest{Name: spec.Name, ToolCallID: "progress-1"}
			result := &planner.ToolResult{Name: spec.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"saved": true}}
			base := &planner.PlanInput{}
			ledger := transcript.NewLedger()
			st := &runLoopState{Ledger: ledger}
			rt.recordAssistantTurn(base, nil, []planner.ToolRequest{call}, ledger)

			var err error
			if executed {
				err = rt.recordExecutedConfirmationResults(context.Background(), base, st, call, []*planner.ToolResult{result})
			} else {
				err = rt.recordConfirmationToolResult(context.Background(), base, st, call, result)
			}

			require.NoError(t, err)
			require.Empty(t, base.Messages)
			require.Len(t, ledger.BuildMessages(), 2)
		})
	}
}

func TestBookkeepingHidingScansAllPendingAssistantMessages(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	spec := bookkeepingSpec("control.progress", false)
	rt.toolSpecs[spec.Name] = spec
	first := planner.ToolRequest{Name: spec.Name, ToolCallID: "progress-1"}
	second := planner.ToolRequest{Name: spec.Name, ToolCallID: "progress-2"}
	firstResult := &planner.ToolResult{Name: spec.Name, ToolCallID: first.ToolCallID, Result: map[string]any{"saved": true}}
	secondResult := &planner.ToolResult{Name: spec.Name, ToolCallID: second.ToolCallID, Result: map[string]any{"saved": true}}
	base := &planner.PlanInput{}
	ledger := transcript.NewLedger()
	rt.recordAssistantTurn(base, nil, []planner.ToolRequest{first}, ledger)
	rt.recordAssistantTurn(base, nil, []planner.ToolRequest{second}, ledger)

	rt.hideSuccessfulBookkeepingCallsFromPlanner(base, []*planner.ToolResult{firstResult})
	require.Zero(t, countToolUseParts(base.Messages, first.ToolCallID))
	require.Equal(t, 1, countToolUseParts(base.Messages, second.ToolCallID))

	rt.hideSuccessfulBookkeepingCallsFromPlanner(base, []*planner.ToolResult{secondResult})
	require.Empty(t, base.Messages)
}

func TestProvidedBookkeepingStaysInTranscriptButNotPlannerContext(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	spec := bookkeepingSpec("control.progress", false)
	rt.toolSpecs[spec.Name] = spec
	call := planner.ToolRequest{Name: spec.Name, ToolCallID: "progress-1"}
	result := &planner.ToolResult{Name: spec.Name, ToolCallID: call.ToolCallID, Result: map[string]any{"saved": true}}
	base := &planner.PlanInput{}
	ledger := transcript.NewLedger()
	st := &runLoopState{Ledger: ledger}
	rt.recordAssistantTurn(base, nil, []planner.ToolRequest{call}, ledger)

	err := rt.recordProvidedToolResults(context.Background(), base, st, []planner.ToolRequest{call}, []*planner.ToolResult{result})

	require.NoError(t, err)
	require.Empty(t, base.Messages)
	require.Len(t, ledger.BuildMessages(), 2)
}

func TestSuccessfulBookkeepingDoesNotResetRecoveryBudget(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	bookkeeping := bookkeepingSpec("control.progress", false)
	domain := newAnyJSONSpec("domain.lookup", "domain")
	rt.toolSpecs[bookkeeping.Name] = bookkeeping
	rt.toolSpecs[domain.Name] = domain
	caps := policy.CapsState{MaxRecoveryTurns: 2, RemainingRecoveryTurns: 0}

	resetRecoveryTurnsAfterResults(rt, &caps, []*planner.ToolResult{{Name: bookkeeping.Name}})
	require.Zero(t, caps.RemainingRecoveryTurns)
	resetRecoveryTurnsAfterResults(rt, &caps, []*planner.ToolResult{{Name: domain.Name}})
	require.Equal(t, 2, caps.RemainingRecoveryTurns)
}

func TestRegisterToolsetRejectsTerminalToolWithoutBookkeeping(t *testing.T) {
	rt := New(WithLogger(telemetry.NoopLogger{}))
	spec := newAnyJSONSpec("control.complete", "control")
	spec.TerminalRun = true

	err := rt.RegisterToolset(ToolsetRegistration{
		Name: "control",
		Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
			return nil, nil
		},
		Specs: []tools.ToolSpec{spec},
	})

	require.ErrorIs(t, err, ErrInvalidConfig)
	require.ErrorContains(t, err, `terminal tool "control.complete" must also declare bookkeeping`)
}

func bookkeepingSpec(name tools.Ident, terminal bool) tools.ToolSpec {
	spec := newAnyJSONSpec(name, "control")
	spec.Bookkeeping = true
	spec.TerminalRun = terminal
	return spec
}
