package runtime

import (
	"context"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// finalizationActivityCalls applies the terminal policy and canonical metadata
// before storing a model-produced or fixed terminal batch. It runs in activities.
func (r *Runtime) finalizationActivityCalls(ctx context.Context, in *PlanActivityInput, calls []planner.ToolRequest) ([]planner.ToolRequest, error) {
	reg := AgentRegistration{ID: in.AgentID}
	for _, call := range calls {
		if !slices.Contains(in.AllowedTools, call.Name) {
			return nil, fmt.Errorf("finalization terminal tool plan is excluded by the run policy")
		}
		if spec, ok := r.toolSpec(call.Name); ok {
			reg.Specs = append(reg.Specs, spec)
		}
	}
	base := &workflowPlanInput{AgentID: in.AgentID, RunContext: in.RunContext}
	input := &RunInput{AgentID: in.AgentID, Labels: cloneLabels(in.RunContext.Labels), Policy: &PolicyOverrides{PerTurnMaxToolCalls: in.FinalizeMaxToolCalls}}
	return r.prepareFinalizationToolCalls(ctx, reg, input, base, calls, in.PolicyCaps, in.RunContext.TurnID, in.RunContext.Attempt, in.Finalize.Reason)
}

func (r *Runtime) recordFinalizationCalls(ctx context.Context, input *api.RecordContentInput) (*api.RecordContentOutput, error) {
	fixed := input.FinalizeCalls
	calls, err := r.finalizationActivityCalls(ctx, &fixed.Input, fixed.Calls)
	if err != nil {
		return nil, err
	}
	out, err := r.storePlanActivityOutput(ctx, input.RootRunID, &PlanActivityOutput{Result: &planner.PlanResult{ToolCalls: calls}}, nil, nil)
	if err != nil {
		return nil, err
	}
	return &api.RecordContentOutput{Calls: calls, CallRefs: out.ToolCallRefs}, nil
}

func (r *Runtime) storedFinalizationCalls(wf engine.WorkflowContext, reg AgentRegistration, input *RunInput, base *workflowPlanInput, calls []planner.ToolRequest, caps policy.CapsState, turn string, attempt int, reason planner.TerminationReason) ([]planner.ToolRequest, error) {
	stored := len(calls) > 0
	for _, call := range calls {
		stored = stored && base.CallRefs[call.ToolCallID] != ""
	}
	if stored {
		return calls, nil
	}
	// Fixed limit plans do not originate in a planner activity. Use the same
	// preparation in the content activity, and adopt only its stored calls.
	rc := base.RunContext
	rc.TurnID, rc.Attempt = turn, attempt
	req := PlanActivityInput{AgentID: input.AgentID, RunContext: rc, AllowedTools: r.terminalFinalizerToolNames(reg, input), PolicyCaps: caps, Finalize: &planner.Termination{Reason: reason}}
	if input.Policy != nil {
		req.FinalizeMaxToolCalls = input.Policy.PerTurnMaxToolCalls
	}
	out, err := wf.ExecuteRecordContentActivity(wf.Context(), engine.RecordContentActivityCall{Name: RecordContentActivityName(input.AgentID), Input: &api.RecordContentInput{RootRunID: rc.RootRunID, FinalizeCalls: &api.FinalizationContent{Input: req, Calls: calls}}})
	if err != nil {
		return nil, err
	}
	if out == nil || len(out.Calls) != len(calls) || len(out.CallRefs) != len(calls) {
		return nil, fmt.Errorf("invalid stored finalization calls")
	}
	base.CallRefs = make(map[string]runstore.Hash, len(calls))
	for i, call := range out.Calls {
		base.CallRefs[call.ToolCallID] = out.CallRefs[i]
	}
	return out.Calls, nil
}
