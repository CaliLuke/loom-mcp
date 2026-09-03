package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

const (
	// FinalizationReasonLabel records why the runtime is executing a terminal
	// tool during finalization. The runtime overwrites this reserved label after
	// caller, planner, and policy labels have been applied.
	FinalizationReasonLabel = "loom-mcp.finalization_reason"
)

func cloneLimitTerminalPlans(plans *LimitTerminalPlans) *LimitTerminalPlans {
	if plans == nil {
		return nil
	}
	return &LimitTerminalPlans{
		TimeBudget:  cloneLimitTerminalCall(plans.TimeBudget),
		ToolCallCap: cloneLimitTerminalCall(plans.ToolCallCap),
		RecoveryCap: cloneLimitTerminalCall(plans.RecoveryCap),
	}
}

func cloneLimitTerminalCall(call LimitTerminalCall) LimitTerminalCall {
	return LimitTerminalCall{
		Name:    call.Name,
		Payload: append(rawjson.Message(nil), call.Payload...),
	}
}

func (r *Runtime) validateTerminalPolicies(reg AgentRegistration, input *RunInput) error {
	if input == nil {
		return errors.New("run input is required")
	}
	if err := r.validateCompletionToolPolicy(reg, input.Policy); err != nil {
		return err
	}
	if err := validateCompletionToolWorkflowRetry(input.Policy, input.WorkflowOptions); err != nil {
		return err
	}
	if input.Policy == nil {
		return nil
	}
	return r.validateLimitTerminalPlans(reg, input.Policy.LimitTerminalPlans)
}

func (r *Runtime) validateCompletionToolPolicy(reg AgentRegistration, runPolicy *PolicyOverrides) error {
	completion := completionToolFromPolicy(runPolicy)
	if completion == "" {
		return nil
	}
	if runPolicy.LimitTerminalPlans != nil {
		return errors.New("completion tool and limit terminal plans cannot be combined")
	}
	spec, ok := agentToolSpec(reg.Specs, completion)
	if !ok {
		return fmt.Errorf("completion tool %q is not registered for agent %q", completion, reg.ID)
	}
	if spec.TerminalRun {
		return fmt.Errorf("completion tool %q must not be a terminal tool", completion)
	}
	if spec.Bookkeeping {
		return fmt.Errorf("completion tool %q must be budgeted", completion)
	}
	if spec.Confirmation != nil || r.hasRuntimeConfirmation(completion) {
		return fmt.Errorf("completion tool %q cannot require confirmation", completion)
	}
	if !toolAllowedByRunPolicy(spec, runPolicy) {
		return fmt.Errorf("completion tool %q is excluded by the run tool policy", completion)
	}
	return nil
}

func validateCompletionToolWorkflowRetry(runPolicy *PolicyOverrides, opts *WorkflowOptions) error {
	if completionToolFromPolicy(runPolicy) == "" || opts == nil {
		return nil
	}
	retry := opts.RetryPolicy
	if retry.MaxAttempts != 0 || retry.InitialInterval != 0 || retry.BackoffCoefficient != 0 {
		return errors.New("completion tool runs cannot configure whole-workflow retries")
	}
	return nil
}

func (r *Runtime) validateCompletionToolPlanResult(result *planner.PlanResult, completion tools.Ident) error {
	if completion == "" || result == nil {
		return nil
	}
	if result.FinalResponse != nil || result.FinalToolResult != nil {
		return completionToolRequiredError(completion, "planner returned a terminal response")
	}
	for _, call := range result.ToolCalls {
		if call.Name == completion && (len(result.ToolCalls) != 1 || result.Await != nil) {
			return fmt.Errorf("completion tool %q must be the only action in its planner response", completion)
		}
		spec, ok := r.toolSpec(call.Name)
		if ok && spec.TerminalRun {
			return completionToolRequiredError(completion, fmt.Sprintf("planner selected terminal tool %q", call.Name))
		}
	}
	if result.Await != nil {
		for _, item := range result.Await.Items {
			if item.Questions != nil && item.Questions.ToolName == completion {
				return completionToolRequiredError(completion, "planner delegated its execution to await work")
			}
			if item.ExternalTools == nil {
				continue
			}
			for _, call := range item.ExternalTools.Items {
				if call.Name == completion {
					return completionToolRequiredError(completion, "planner delegated its execution to await work")
				}
			}
		}
	}
	return nil
}

func completionTool(input *RunInput) tools.Ident {
	if input == nil {
		return ""
	}
	return completionToolFromPolicy(input.Policy)
}

func completionToolFromPolicy(runPolicy *PolicyOverrides) tools.Ident {
	if runPolicy == nil {
		return ""
	}
	return runPolicy.CompletionTool
}

func completionToolSucceeded(results []*planner.ToolResult, completion tools.Ident) bool {
	if completion == "" {
		return false
	}
	for _, result := range results {
		if result == nil || result.Name != completion {
			continue
		}
		return result.Error == nil
	}
	return false
}

func completionToolRequiredError(completion tools.Ident, reason any) error {
	return fmt.Errorf("completion tool %q did not succeed: %v", completion, reason)
}

func (r *Runtime) validateLimitTerminalPlans(reg AgentRegistration, plans *LimitTerminalPlans) error {
	if plans == nil {
		return nil
	}
	for _, entry := range []struct {
		reason planner.TerminationReason
		call   LimitTerminalCall
	}{
		{reason: planner.TerminationReasonTimeBudget, call: plans.TimeBudget},
		{reason: planner.TerminationReasonToolCap, call: plans.ToolCallCap},
		{reason: planner.TerminationReasonFailureCap, call: plans.RecoveryCap},
	} {
		if err := r.validateLimitTerminalCall(reg, entry.call); err != nil {
			return fmt.Errorf("runtime: invalid %s terminal call: %w", entry.reason, err)
		}
	}
	return nil
}

func (r *Runtime) validateLimitTerminalCall(reg AgentRegistration, call LimitTerminalCall) error {
	spec, ok := agentToolSpec(reg.Specs, call.Name)
	if !ok {
		return fmt.Errorf("tool %q is not registered for agent %q", call.Name, reg.ID)
	}
	if !spec.Bookkeeping || !spec.TerminalRun {
		return fmt.Errorf("tool %q is not a terminal bookkeeping tool", call.Name)
	}
	if spec.Confirmation != nil || r.hasRuntimeConfirmation(call.Name) {
		return fmt.Errorf("tool %q requires confirmation", call.Name)
	}
	if len(call.Payload) == 0 {
		return fmt.Errorf("tool %q payload is required", call.Name)
	}
	if spec.Payload.Codec.FromJSON == nil {
		return fmt.Errorf("tool %q payload decoder is required", call.Name)
	}
	if _, err := spec.Payload.Codec.FromJSON(call.Payload); err != nil {
		return fmt.Errorf("tool %q payload: %w", call.Name, err)
	}
	return nil
}

func limitTerminalCall(plans *LimitTerminalPlans, reason planner.TerminationReason) (LimitTerminalCall, bool, error) {
	if plans == nil {
		return LimitTerminalCall{}, false, nil
	}
	switch reason {
	case planner.TerminationReasonTimeBudget:
		return cloneLimitTerminalCall(plans.TimeBudget), true, nil
	case planner.TerminationReasonToolCap:
		return cloneLimitTerminalCall(plans.ToolCallCap), true, nil
	case planner.TerminationReasonFailureCap:
		return cloneLimitTerminalCall(plans.RecoveryCap), true, nil
	default:
		return LimitTerminalCall{}, false, fmt.Errorf("unsupported termination reason %q", reason)
	}
}

func agentToolSpec(specs []tools.ToolSpec, name tools.Ident) (tools.ToolSpec, bool) {
	for _, spec := range specs {
		if spec.Name == name {
			return spec, true
		}
	}
	return tools.ToolSpec{}, false
}

func (r *Runtime) hasRuntimeConfirmation(name tools.Ident) bool {
	if r.toolConfirmation == nil {
		return false
	}
	_, ok := r.toolConfirmation.Confirm[name]
	return ok
}

func toolAllowedByRunPolicy(spec tools.ToolSpec, runPolicy *PolicyOverrides) bool {
	if runPolicy == nil {
		return true
	}
	if runPolicy.RestrictToTool != "" && runPolicy.RestrictToTool != spec.Name {
		return false
	}
	if len(runPolicy.AllowedTags) > 0 && !hasIntersection(spec.Tags, runPolicy.AllowedTags) {
		return false
	}
	return len(runPolicy.DeniedTags) == 0 || !hasIntersection(spec.Tags, runPolicy.DeniedTags)
}

func stampFinalizationReason(calls []planner.ToolRequest, reason planner.TerminationReason) {
	for i := range calls {
		labels := cloneLabels(calls[i].Labels)
		if labels == nil {
			labels = make(map[string]string, 1)
		}
		labels[FinalizationReasonLabel] = string(reason)
		calls[i].Labels = labels
	}
}

func (r *Runtime) terminalFinalizerToolNames(reg AgentRegistration, input *RunInput) []tools.Ident {
	names := make([]tools.Ident, 0)
	for _, spec := range reg.Specs {
		if !spec.TerminalRun || !spec.Bookkeeping || spec.Confirmation != nil ||
			r.hasRuntimeConfirmation(spec.Name) || !toolAllowedByRunPolicy(spec, input.Policy) {
			continue
		}
		names = append(names, spec.Name)
	}
	slices.Sort(names)
	return names
}

func (r *Runtime) prepareFinalizationToolCalls(
	ctx context.Context,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
	calls []planner.ToolRequest,
	caps policy.CapsState,
	turnID string,
	nextAttempt int,
	reason planner.TerminationReason,
) ([]planner.ToolRequest, error) {
	if err := r.validateFinalizationToolCalls(reg, calls); err != nil {
		return nil, err
	}
	delete(base.RunContext.Labels, FinalizationReasonLabel)
	for i := range calls {
		calls[i].ToolCallID = generateDeterministicToolCallID(base.RunContext.RunID, turnID, nextAttempt, calls[i].Name, i)
		delete(calls[i].Labels, FinalizationReasonLabel)
	}

	filtered := r.applyPerRunOverrides(ctx, input, calls)
	if len(filtered) != len(calls) {
		return nil, errors.New("finalization terminal tool plan is excluded by the run policy")
	}
	decision, err := r.applyPolicy(ctx, base, input, filtered, caps, turnID, nil, toolPolicyEnvelope{})
	if err != nil {
		return nil, err
	}
	if len(decision.AllowedCalls) != len(calls) {
		return nil, errors.New("finalization terminal tool plan is excluded by runtime policy")
	}
	prepared := r.prepareAllowedCallsMetadata(input.AgentID, base, decision.AllowedCalls, nil)
	stampFinalizationReason(prepared, reason)
	return prepared, nil
}

func (r *Runtime) executeFinalizationToolCalls(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
	calls []planner.ToolRequest,
	allToolResults []*planner.ToolResult,
	aggUsage model.TokenUsage,
	caps policy.CapsState,
	nextAttempt int,
	turnID string,
	reason planner.TerminationReason,
	hardDeadline time.Time,
) (*RunOutput, error) {
	prepared, err := r.prepareFinalizationToolCalls(
		wfCtx.Context(), reg, input, base, calls, caps, turnID, nextAttempt, reason,
	)
	if err != nil {
		return nil, err
	}
	results, err := r.runPreparedFinalizationToolCalls(wfCtx, reg, input, base, prepared, hardDeadline)
	if err != nil {
		return nil, err
	}
	allToolResults = append(allToolResults, results...)
	events, err := r.encodeToolEvents(wfCtx.Context(), allToolResults)
	if err != nil {
		return nil, err
	}
	return &RunOutput{
		AgentID:         input.AgentID,
		RunID:           base.RunContext.RunID,
		FinalToolResult: events[len(events)-1],
		ToolEvents:      events,
		Usage:           &aggUsage,
	}, nil
}

func (r *Runtime) validateFinalizationToolCalls(reg AgentRegistration, calls []planner.ToolRequest) error {
	if len(calls) == 0 {
		return errors.New("finalization terminal tool plan has no tool calls")
	}
	for _, call := range calls {
		if err := r.validateFinalizationToolCall(reg, call); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) validateFinalizationToolCall(reg AgentRegistration, call planner.ToolRequest) error {
	spec, ok := agentToolSpec(reg.Specs, call.Name)
	if !ok {
		return fmt.Errorf("finalization terminal tool plan cannot call tool %q outside agent %q", call.Name, reg.ID)
	}
	if !spec.TerminalRun || !spec.Bookkeeping {
		return fmt.Errorf("finalization terminal tool plan cannot call non-terminal tool %q", call.Name)
	}
	if spec.Confirmation != nil || r.hasRuntimeConfirmation(spec.Name) {
		return fmt.Errorf("finalization terminal tool %q requires confirmation", call.Name)
	}
	if spec.Payload.Codec.FromJSON == nil {
		return fmt.Errorf("finalization terminal tool %q has no payload decoder", call.Name)
	}
	if _, err := spec.Payload.Codec.FromJSON(call.Payload); err != nil {
		return fmt.Errorf("finalization terminal tool %q payload: %w", call.Name, err)
	}
	return nil
}

func (r *Runtime) runPreparedFinalizationToolCalls(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
	prepared []planner.ToolRequest,
	hardDeadline time.Time,
) ([]*planner.ToolResult, error) {
	_, toolOpts := resolveRunLoopActivityOptions(reg, input)
	outcomes, timedOut, err := r.executeToolCalls(
		wfCtx,
		reg.ExecuteToolActivity,
		toolOpts,
		input.AgentID,
		&base.RunContext,
		base.Messages,
		prepared,
		0,
		nil,
		hardDeadline,
	)
	if err != nil {
		return nil, err
	}
	if timedOut {
		return nil, fmt.Errorf("finalization terminal tool step timed out: %w", context.DeadlineExceeded)
	}
	if len(toolPausesFromExecutions(outcomes)) > 0 {
		return nil, errors.New("finalization terminal tool must not request a pause")
	}
	results := toolResultsFromExecutions(outcomes)
	if err := validateFinalizationToolResults(results, len(prepared)); err != nil {
		return nil, err
	}
	return results, nil
}

func validateFinalizationToolResults(results []*planner.ToolResult, expected int) error {
	if len(results) != expected {
		return errors.New("finalization terminal tool step returned incomplete results")
	}
	for _, result := range results {
		if result == nil {
			return errors.New("finalization terminal tool returned nil result")
		}
		if result.Error != nil {
			return fmt.Errorf("finalization terminal tool %q failed: %s", result.Name, result.Error.Message)
		}
	}
	return nil
}
