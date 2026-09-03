package runtime

import (
	"context"
	"encoding/json/v2"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func toPolicyRetryHint(hint *planner.RetryHint) *policy.RetryHint {
	if hint == nil {
		return nil
	}
	return &policy.RetryHint{
		Reason:             policy.RetryReason(hint.Reason),
		Tool:               hint.Tool,
		RestrictToTool:     hint.RestrictToTool,
		MissingFields:      cloneStrings(hint.MissingFields),
		ExampleInput:       cloneMetadata(hint.ExampleInput),
		PriorInput:         cloneMetadata(hint.PriorInput),
		ClarifyingQuestion: hint.ClarifyingQuestion,
		Message:            hint.Message,
	}
}

// applyHistoryPolicy applies the agent's history policy to the given messages.
func (r *Runtime) applyHistoryPolicy(ctx context.Context, reg *AgentRegistration, msgs []*model.Message) ([]*model.Message, error) {
	if reg.Policy.History == nil || len(msgs) == 0 {
		return msgs, nil
	}
	out, err := reg.Policy.History(ctx, msgs, toolDefinitionsForHistory(reg.Specs))
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return msgs, nil
	}
	return out, nil
}

func toolDefinitionsForHistory(specs []tools.ToolSpec) []*model.ToolDefinition {
	if len(specs) == 0 {
		return nil
	}
	defs := make([]*model.ToolDefinition, 0, len(specs))
	for _, spec := range specs {
		defs = append(defs, &model.ToolDefinition{
			Name:        spec.Name.String(),
			Description: spec.Description,
			InputSchema: historyToolInputSchema(spec),
		})
	}
	return defs
}

func historyToolInputSchema(spec tools.ToolSpec) any {
	if len(spec.Payload.Schema) == 0 {
		return map[string]any{jsonSchemaTypeKey: jsonSchemaTypeValue}
	}
	var schema any
	if err := json.Unmarshal(spec.Payload.Schema, &schema); err != nil {
		return map[string]any{jsonSchemaTypeKey: jsonSchemaTypeValue}
	}
	return schema
}

// initialCaps constructs the initial caps state from the agent's run policy.
func initialCaps(cfg RunPolicy) policy.CapsState {
	maxRecoveryTurns := cfg.MaxRecoveryTurns
	if maxRecoveryTurns < 0 {
		maxRecoveryTurns = 0
	} else if maxRecoveryTurns == 0 {
		maxRecoveryTurns = policy.DefaultMaxRecoveryTurns
	}
	caps := policy.CapsState{
		MaxToolCalls:     cfg.MaxToolCalls,
		MaxRecoveryTurns: maxRecoveryTurns,
	}
	if cfg.MaxToolCalls > 0 {
		caps.RemainingToolCalls = cfg.MaxToolCalls
	}
	caps.RemainingRecoveryTurns = maxRecoveryTurns
	return caps
}

// decrementCap decrements a cap value by delta.
func decrementCap(current int, delta int) int {
	if current == 0 || delta == 0 {
		return current
	}
	result := current - delta
	if result < 0 {
		return 0
	}
	return result
}

// mergeCaps merges policy decision caps into the current caps state.
func mergeCaps(current policy.CapsState, decision policy.CapsState) policy.CapsState {
	if decision.MaxToolCalls > 0 {
		current.MaxToolCalls = decision.MaxToolCalls
	}
	if decision.RemainingToolCalls > 0 {
		current.RemainingToolCalls = decision.RemainingToolCalls
	}
	if decision.MaxRecoveryTurns > 0 {
		current.MaxRecoveryTurns = decision.MaxRecoveryTurns
	}
	if decision.RemainingRecoveryTurns > 0 {
		current.RemainingRecoveryTurns = decision.RemainingRecoveryTurns
	}
	if current.MaxRecoveryTurns > 0 && current.RemainingRecoveryTurns > current.MaxRecoveryTurns {
		current.RemainingRecoveryTurns = current.MaxRecoveryTurns
	}
	return current
}

// toolHandles converts tool call requests into policy tool handles.
func toolHandles(calls []planner.ToolRequest) []tools.Ident {
	handles := make([]tools.Ident, len(calls))
	for i, call := range calls {
		handles[i] = call.Name
	}
	return handles
}

// hasIntersection reports whether two string slices share at least one common value.
func hasIntersection(a []string, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, v := range a {
		set[v] = struct{}{}
	}
	for _, v := range b {
		if _, ok := set[v]; ok {
			return true
		}
	}
	return false
}

// toolMetadata retrieves policy metadata for each tool call.
func (r *Runtime) toolMetadata(calls []planner.ToolRequest) []policy.ToolMetadata {
	metas := make([]policy.ToolMetadata, 0, len(calls))
	for _, call := range calls {
		if spec, ok := r.toolSpec(call.Name); ok {
			budgetClass := policy.ToolBudgetClassBudgeted
			if spec.Bookkeeping {
				budgetClass = policy.ToolBudgetClassBookkeeping
			}
			metas = append(metas, policy.ToolMetadata{
				ID:          spec.Name,
				Title:       defaultToolTitle(spec.Name),
				Description: spec.Description,
				Tags:        append([]string(nil), spec.Tags...),
				BudgetClass: budgetClass,
			})
			continue
		}
		metas = append(metas, policy.ToolMetadata{
			ID:          call.Name,
			Title:       defaultToolTitle(call.Name),
			BudgetClass: policy.ToolBudgetClassBudgeted,
		})
	}
	return metas
}

// filterToolCalls filters tool calls to only those present in the allowed list.
// An empty allowed list permits nothing. Callers that treat "no allowlist" as
// unrestricted (see allowedPolicyCalls) must handle that case before calling.
func filterToolCalls(calls []planner.ToolRequest, allowed []tools.Ident) []planner.ToolRequest {
	allow := make(map[tools.Ident]struct{}, len(allowed))
	for _, id := range allowed {
		allow[id] = struct{}{}
	}
	filtered := make([]planner.ToolRequest, 0, len(calls))
	for _, call := range calls {
		if _, ok := allow[call.Name]; ok {
			filtered = append(filtered, call)
		}
	}
	return filtered
}
