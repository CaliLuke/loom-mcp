package runtime

import (
	"encoding/json/v2"
	"fmt"
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/stretchr/testify/require"
)

func TestToolResultContentTruncatesOversizedResults(t *testing.T) {
	rt := newTestRuntimeWithPlanner("service.agent", &stubPlanner{})
	name := tools.Ident("svc.ts.big")
	rt.toolSpecs[name] = newAnyJSONSpec(name, "svc.ts")

	tr := &planner.ToolResult{
		Name:       name,
		ToolCallID: "tc-1",
		Result: map[string]any{
			"blob": strings.Repeat("x", transcript.MaxToolResultContentBytes+1024),
		},
	}

	content, err := rt.toolResultContent(tr)
	require.NoError(t, err)
	m, ok := content.(map[string]any)
	require.True(t, ok, "oversized tool_result content must be projected, not raw JSON")
	require.Equal(t, true, m["omitted"])
	require.Equal(t, "size_limit", m["reason"])
}

func TestEnforcePlanActivityInputBudgetFailsFast(t *testing.T) {
	in := PlanActivityInput{
		RunID:      "run-1",
		RunContext: run.Context{RootRunID: "test-root", Labels: map[string]string{"oversized": strings.Repeat("x", maxPlanActivityInputBytes+1024)}},
	}
	var oversized *PlanActivityInputBudgetError
	require.ErrorAs(t, enforcePlanActivityInputBudget(in), &oversized)
	require.Greater(t, oversized.Bytes, oversized.Limit)
}

func TestPlanActivityInputBudgetErrorBoundary(t *testing.T) {
	in := PlanActivityInput{RunID: "budget-run", RunContext: run.Context{Labels: map[string]string{"data": ""}}}
	data, err := json.Marshal(in)
	require.NoError(t, err)
	in.RunContext.Labels["data"] = strings.Repeat("x", maxPlanActivityInputBytes-len(data))
	require.NoError(t, enforcePlanActivityInputBudget(in), "exactly 1 MB is allowed")
	in.RunContext.Labels["data"] += "x"
	err = fmt.Errorf("schedule planner: %w", enforcePlanActivityInputBudget(in))
	var budget *PlanActivityInputBudgetError
	require.ErrorAs(t, err, &budget)
	assert.Equal(t, maxPlanActivityInputBytes+1, budget.Bytes)
	assert.Equal(t, maxPlanActivityInputBytes, budget.Limit)
	assert.Equal(t, in.RunID, budget.RunID)
	for _, removed := range []string{"Messages", "ToolOutputs", "TypedInputs", "Ledger"} {
		assert.NotContains(t, budget.Error(), removed)
	}
}
