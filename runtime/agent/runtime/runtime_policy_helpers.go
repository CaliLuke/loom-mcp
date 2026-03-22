package runtime

import (
	"context"

	"goa.design/goa-ai/runtime/agent/model"
	"goa.design/goa-ai/runtime/agent/planner"
	"goa.design/goa-ai/runtime/agent/policy"
	"goa.design/goa-ai/runtime/agent/tools"
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
func (r *Runtime) applyHistoryPolicy(ctx context.Context, reg *AgentRegistration, msgs []*model.Message) []*model.Message {
	if reg.Policy.History == nil || len(msgs) == 0 {
		return msgs
	}
	out, err := reg.Policy.History(ctx, msgs)
	if err != nil {
		r.logWarn(ctx, "history policy failed", err, "agent_id", reg.ID)
		return msgs
	}
	if len(out) == 0 {
		return msgs
	}
	return out
}

// initialCaps constructs the initial caps state from the agent's run policy.
func initialCaps(cfg RunPolicy) policy.CapsState {
	caps := policy.CapsState{
		MaxToolCalls:                  cfg.MaxToolCalls,
		MaxConsecutiveFailedToolCalls: cfg.MaxConsecutiveFailedToolCalls,
	}
	if cfg.MaxToolCalls > 0 {
		caps.RemainingToolCalls = cfg.MaxToolCalls
	}
	if cfg.MaxConsecutiveFailedToolCalls > 0 {
		caps.RemainingConsecutiveFailedToolCalls = cfg.MaxConsecutiveFailedToolCalls
	}
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

// capFailures counts tool failures that should decrement the consecutive-failure cap.
func capFailures(results []*planner.ToolResult) int {
	count := 0
	for _, res := range results {
		if res == nil || res.Error == nil {
			continue
		}
		if h := res.RetryHint; h != nil {
			switch h.Reason {
			case planner.RetryReasonMissingFields, planner.RetryReasonInvalidArguments, planner.RetryReasonToolUnavailable:
				continue
			case planner.RetryReasonMalformedResponse,
				planner.RetryReasonTimeout,
				planner.RetryReasonRateLimited:
			default:
			}
		}
		count++
	}
	return count
}

// mergeCaps merges policy decision caps into the current caps state.
func mergeCaps(current policy.CapsState, decision policy.CapsState) policy.CapsState {
	if decision.MaxToolCalls > 0 {
		current.MaxToolCalls = decision.MaxToolCalls
	}
	if decision.RemainingToolCalls > 0 {
		current.RemainingToolCalls = decision.RemainingToolCalls
	}
	if decision.MaxConsecutiveFailedToolCalls > 0 {
		current.MaxConsecutiveFailedToolCalls = decision.MaxConsecutiveFailedToolCalls
	}
	if decision.RemainingConsecutiveFailedToolCalls > 0 {
		current.RemainingConsecutiveFailedToolCalls = decision.RemainingConsecutiveFailedToolCalls
	}
	if !decision.ExpiresAt.IsZero() {
		current.ExpiresAt = decision.ExpiresAt
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
			metas = append(metas, policy.ToolMetadata{
				ID:          spec.Name,
				Title:       defaultToolTitle(spec.Name),
				Description: spec.Description,
				Tags:        append([]string(nil), spec.Tags...),
			})
			continue
		}
		metas = append(metas, policy.ToolMetadata{
			ID:    call.Name,
			Title: defaultToolTitle(call.Name),
		})
	}
	return metas
}

// filterToolCalls filters tool calls to only those present in the allowed list.
func filterToolCalls(calls []planner.ToolRequest, allowed []tools.Ident) []planner.ToolRequest {
	if len(allowed) == 0 {
		return calls
	}
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
