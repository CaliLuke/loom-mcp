package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type unsafeToolCallPlanner struct {
	call planner.ToolRequest
}

func (p unsafeToolCallPlanner) PlanStart(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{p.call}}, nil
}

func (unsafeToolCallPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, nil
}

func TestRewriteUnknownToolCallsRejectsBatchWithoutRejectedModelContent(t *testing.T) {
	t.Parallel()

	rt := &Runtime{toolSpecs: map[tools.Ident]tools.ToolSpec{
		"svc.read": {Name: "svc.read"},
	}}
	calls := []planner.ToolRequest{
		{Name: "svc.read", ToolCallID: "valid", Payload: rawjson.Message(`{"query":"public"}`)},
		{Name: "secret.unadvertised", ToolCallID: "rejected", Payload: rawjson.Message(`{"token":"secret-value"}`)},
	}

	rewritten := rt.rewriteUnknownToolCalls(calls, toolPolicyEnvelope{
		Active:  true,
		Allowed: []tools.Ident{"svc.read"},
	})
	require.Len(t, rewritten, 1, "the complete mixed batch must be rejected before valid sibling execution")
	require.Equal(t, tools.ToolUnavailable, rewritten[0].Name)
	require.Equal(t, "rejected", rewritten[0].ToolCallID)
	require.JSONEq(t, `{"available_tools":["svc.read"]}`, string(rewritten[0].Payload))
	require.NotContains(t, string(rewritten[0].Payload), "secret.unadvertised")
	require.NotContains(t, string(rewritten[0].Payload), "secret-value")

	result, err := executeToolUnavailable(context.Background(), &rewritten[0])
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ToolResult)
	require.NotNil(t, result.ToolResult.Error)
	require.NotContains(t, result.ToolResult.Error.Message, "secret.unadvertised")
	require.NotContains(t, result.ToolResult.Error.Message, "secret-value")
	require.Contains(t, result.ToolResult.RetryHint.Message, "svc.read")
}

func TestInternalToolIsNotAPolicyCandidate(t *testing.T) {
	t.Parallel()

	rt := &Runtime{toolSpecs: map[tools.Ident]tools.ToolSpec{
		tools.ToolUnavailable: {Name: tools.ToolUnavailable},
		"svc.read":            {Name: "svc.read"},
	}}
	candidates := rt.policyCandidateCalls(AgentRegistration{})
	require.Equal(t, []planner.ToolRequest{{Name: "svc.read"}}, candidates)
}

func TestPerRunFiltersPreserveInternalToolRecovery(t *testing.T) {
	t.Parallel()

	rt := New()
	tests := []struct {
		name   string
		policy *PolicyOverrides
	}{
		{name: "restrict to domain tool", policy: &PolicyOverrides{RestrictToTool: "svc.read"}},
		{name: "allowed tags", policy: &PolicyOverrides{AllowedTags: []string{"safe"}}},
		{name: "denied tags", policy: &PolicyOverrides{DeniedTags: []string{"internal"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := &RunInput{AgentID: "svc.agent", RunID: "run-recover", Policy: test.policy}
			base := &planner.PlanInput{RunContext: run.Context{RunID: input.RunID, Attempt: 1}}
			st := &runLoopState{
				Result: &planner.PlanResult{ToolCalls: []planner.ToolRequest{{
					Name:       tools.ToolUnavailable,
					ToolCallID: "recover",
					Payload:    rawjson.Message(`{"available_tools":["private.secret"]}`),
				}}},
				ToolPolicy: toolPolicyEnvelope{Active: true, Allowed: []tools.Ident{"svc.read"}},
			}

			allowed, toExecute, confirmations, _, err := rt.prepareToolTurnCalls(
				context.Background(), input, base, st, "turn-1", nil, nil,
			)
			require.NoError(t, err)
			require.Empty(t, confirmations)
			require.Len(t, allowed, 1)
			require.Len(t, toExecute, 1)
			require.Equal(t, tools.ToolUnavailable, toExecute[0].Name)
			require.NotContains(t, string(toExecute[0].Payload), "private.secret")
		})
	}
}

func TestDirectToolUnavailableCallUsesServerCatalog(t *testing.T) {
	t.Parallel()

	rt := &Runtime{toolSpecs: map[tools.Ident]tools.ToolSpec{
		tools.ToolUnavailable: {Name: tools.ToolUnavailable},
		"svc.read":            {Name: "svc.read"},
	}}
	rewritten := rt.rewriteUnknownToolCalls([]planner.ToolRequest{{
		Name:       tools.ToolUnavailable,
		ToolCallID: "direct-internal",
		Payload:    rawjson.Message(`{"available_tools":["private.secret"]}`),
	}}, toolPolicyEnvelope{
		Active:  true,
		Allowed: []tools.Ident{"svc.read", tools.ToolUnavailable},
	})
	require.Len(t, rewritten, 1)
	require.JSONEq(t, `{"available_tools":["svc.read"]}`, string(rewritten[0].Payload))
	require.NotContains(t, string(rewritten[0].Payload), "private.secret")

	execution, err := executeToolUnavailable(context.Background(), &rewritten[0])
	require.NoError(t, err)
	require.NotNil(t, execution)
	require.NotNil(t, execution.ToolResult)
	require.NotContains(t, execution.ToolResult.Error.Error(), "private.secret")
	require.NotContains(t, execution.ToolResult.RetryHint.Message, "private.secret")
	require.Contains(t, execution.ToolResult.RetryHint.Message, "svc.read")
	content, err := rt.toolResultContent(execution.ToolResult)
	require.NoError(t, err)
	require.NotContains(t, fmt.Sprint(content), "private.secret")
}

func TestEmptyToolNameUsesServerOwnedUnavailablePayload(t *testing.T) {
	t.Parallel()

	rt := &Runtime{toolSpecs: map[tools.Ident]tools.ToolSpec{"svc.read": {Name: "svc.read"}}}
	rewritten := rt.rewriteUnknownToolCalls([]planner.ToolRequest{{
		ToolCallID: "empty-name",
		Payload:    rawjson.Message(`{"secret":"private"}`),
	}}, toolPolicyEnvelope{})
	require.Len(t, rewritten, 1)
	require.Equal(t, tools.ToolUnavailable, rewritten[0].Name)
	require.JSONEq(t, `{"available_tools":["svc.read"]}`, string(rewritten[0].Payload))
}

func TestPlanActivityCanonicalizesUnsafeToolCallsBeforePersistence(t *testing.T) {
	tests := []struct {
		name string
		tool tools.Ident
	}{
		{name: "direct internal", tool: tools.ToolUnavailable},
		{name: "empty name"},
		{name: "unknown name", tool: "private.unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rt := New()
			rt.agents["svc.agent"] = AgentRegistration{
				ID: "svc.agent",
				Planner: unsafeToolCallPlanner{call: planner.ToolRequest{
					Name:       test.tool,
					ToolCallID: "unsafe-call",
					Payload:    rawjson.Message(`{"available_tools":["private.secret"]}`),
				}},
			}
			input := &PlanActivityInput{
				AgentID:          "svc.agent",
				RunID:            "run-unsafe-tool",
				RunContext:       run.Context{RunID: "run-unsafe-tool"},
				ToolPolicyActive: true,
				AllowedTools:     []tools.Ident{"svc.read"},
			}

			out, err := rt.PlanStartActivity(context.Background(), input)
			require.NoError(t, err)
			require.NoError(t, validatePlanActivityOutput(out, *input))
			require.Len(t, out.Result.ToolCalls, 1)
			call := out.Result.ToolCalls[0]
			require.Equal(t, tools.ToolUnavailable, call.Name)
			require.JSONEq(t, `{"available_tools":["svc.read"]}`, string(call.Payload))

			persisted, err := json.Marshal(out)
			require.NoError(t, err)
			require.NotContains(t, string(persisted), "private.secret")
			require.NotContains(t, string(persisted), "private.unknown")

			execution, err := executeToolUnavailable(context.Background(), &call)
			require.NoError(t, err)
			require.NotContains(t, execution.ToolResult.RetryHint.Message, "private.secret")
			require.Contains(t, execution.ToolResult.RetryHint.Message, "svc.read")
			content, err := rt.toolResultContent(execution.ToolResult)
			require.NoError(t, err)
			require.NotContains(t, fmt.Sprint(content), "private.secret")
		})
	}
}
