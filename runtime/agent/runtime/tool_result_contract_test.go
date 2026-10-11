package runtime

import (
	"context"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContractErrorTypes(t *testing.T) {
	assert.Equal(t, "planner_contract", PlannerContractErrorType)
	assert.Equal(t, "tool_result_contract", ToolResultContractErrorType)
	err := validatePlannerClarificationQuestions(&planner.PlanResult{Await: planner.NewAwait(
		planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "clarify"}),
	)})
	var contract *engine.ApplicationError
	require.ErrorAs(t, err, &contract)
	assert.Equal(t, PlannerContractErrorType, contract.Type)
	assert.True(t, contract.NonRetryable)
}

func TestToolResultContractViolationsAreNonRetryable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bounds *tools.BoundsSpec
		result *planner.ToolResult
	}{
		{name: "nil result"},
		{name: "result and error", result: &planner.ToolResult{Result: "value", Error: planner.NewToolError("failed")}},
		{name: "missing fields empty question", result: &planner.ToolResult{RetryHint: &planner.RetryHint{Reason: planner.RetryReasonMissingFields}}},
		{name: "missing fields whitespace question", result: &planner.ToolResult{RetryHint: &planner.RetryHint{Reason: planner.RetryReasonMissingFields, ClarifyingQuestion: " \t\n"}}},
		{name: "unbounded with bounds", result: &planner.ToolResult{Bounds: &agent.Bounds{}}},
		{name: "error with bounds", bounds: &tools.BoundsSpec{}, result: &planner.ToolResult{Error: planner.NewToolError("failed"), Bounds: &agent.Bounds{}}},
		{name: "bounded without bounds", bounds: &tools.BoundsSpec{}, result: &planner.ToolResult{}},
		{name: "truncated without continuation", bounds: &tools.BoundsSpec{}, result: &planner.ToolResult{Bounds: &agent.Bounds{Truncated: true}}},
		{name: "cursor without paging", bounds: &tools.BoundsSpec{}, result: &planner.ToolResult{Bounds: &agent.Bounds{NextCursor: new("next")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := newAnyJSONSpec("test.lookup", "test")
			spec.Bounds = tc.bounds
			err := validateToolResultContract(spec, planner.ToolRequest{Name: spec.Name, ToolCallID: "call"}, tc.result)
			assertToolResultContractError(t, err)
		})
	}
}

func TestToolPauseContractViolationsAreNonRetryable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result *planner.ToolResult
		pause  *ToolPause
	}{
		{name: "nil result", pause: &ToolPause{}},
		{name: "error with pause", result: &planner.ToolResult{Error: planner.NewToolError("failed")}, pause: &ToolPause{}},
		{name: "missing clarification", result: &planner.ToolResult{}, pause: &ToolPause{}},
		{name: "missing id", result: &planner.ToolResult{}, pause: &ToolPause{Clarification: &ToolPauseClarification{Question: "Which topic?"}}},
		{name: "empty question", result: &planner.ToolResult{}, pause: &ToolPause{Clarification: &ToolPauseClarification{ID: "clarify"}}},
		{name: "whitespace question", result: &planner.ToolResult{}, pause: &ToolPause{Clarification: &ToolPauseClarification{ID: "clarify", Question: " \t\n\u2003"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateToolPauseContract(planner.ToolRequest{Name: "test.lookup", ToolCallID: "call"}, tc.result, tc.pause)
			assertToolResultContractError(t, err)
		})
	}
}

func TestToolPauseContractPreservesValidQuestion(t *testing.T) {
	question := "  Which topic?\n"
	pause := &ToolPause{Clarification: &ToolPauseClarification{ID: "clarify", Question: question}}
	require.NoError(t, validateToolPauseContract(planner.ToolRequest{}, &planner.ToolResult{}, pause))
	assert.Equal(t, question, pause.Clarification.Question)
}

func TestToolExecutionContractViolationsAreNonRetryable(t *testing.T) {
	for _, tc := range []struct {
		name string
		exec *ToolExecutionResult
	}{
		{name: "nil execution"},
		{name: "nil result", exec: &ToolExecutionResult{}},
		{name: "blank pause", exec: &ToolExecutionResult{ToolResult: &planner.ToolResult{}, Pause: &ToolPause{Clarification: &ToolPauseClarification{ID: "clarify", Question: " \t\n"}}}},
	} {
		for _, path := range []string{"activity", "inline"} {
			t.Run(tc.name+"/"+path, func(t *testing.T) {
				rt := New()
				spec := newAnyJSONSpec("test.lookup", "test")
				reg := ToolsetRegistration{Name: "test", Specs: []tools.ToolSpec{spec}, Execute: func(context.Context, *planner.ToolRequest) (*ToolExecutionResult, error) {
					return tc.exec, nil
				}}
				require.NoError(t, rt.RegisterToolset(reg))
				var err error
				if path == "activity" {
					_, err = rt.ExecuteToolActivity(t.Context(), &ToolInput{RootRunID: "root", RunID: "root", ToolName: spec.Name, ToolCallID: "call"})
				} else {
					exec := &toolBatchExec{r: rt}
					err = exec.dispatchInlineToolCall(&testWorkflowContext{ctx: t.Context()}, newToolCallBatch(nil), planner.ToolRequest{Name: spec.Name, ToolCallID: "call"}, reg)
				}
				assertToolResultContractError(t, err)
			})
		}
	}
}

// assertToolResultContractError checks the exact root as well as errors.As:
// Temporal intentionally does not hoist application errors out of wrappers.
func assertToolResultContractError(t *testing.T, err error) {
	t.Helper()
	var contract *engine.ApplicationError
	require.ErrorAs(t, err, &contract)
	assert.Same(t, contract, err)
	assert.Equal(t, ToolResultContractErrorType, contract.Type)
	assert.True(t, contract.NonRetryable)
}
