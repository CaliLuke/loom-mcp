package runtime

import (
	"encoding/json/v2"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
)

type recallCharge struct {
	kind           recall.Kind
	exhausted      bool
	otherRemaining int
	remaining      compaction.RecallAllowances
}

// prepareRecall includes eviction intents: their checkpoint address is filled by
// the loader before the model sees this first shortened view.
func prepareRecall(base *workflowPlanInput, input *PlanActivityInput) {
	base.recallCharges = nil
	shortened := input.View.Checkpoint != nil || input.View.ClearedThrough > 0 || input.View.TailFrom > 1
	base.recallAdvertised = base.recallAdvertised || shortened
	base.recallAvailable = false
	input.ExtraTools = nil
	if input.ToolsDisabled || base.compaction == nil || base.recovery.DisableRecall || !base.recallAdvertised || input.Finalize != nil || (input.Recovery != nil && input.Recovery.DisableTools) {
		return
	}
	spec := recall.Spec()
	var schema any
	if err := json.Unmarshal(spec.Payload.Schema, &schema); err != nil {
		panic(err)
	}
	input.ExtraTools = []*model.ToolDefinition{{Name: spec.Name.String(), Description: spec.Description, InputSchema: schema}}
	if input.ToolPolicyActive && !slices.Contains(input.AllowedTools, tools.RecallContext) {
		input.AllowedTools = append(input.AllowedTools, tools.RecallContext)
	}
	base.recallAvailable = true
}

// chargeRecallCalls runs before timeout grouping, preserving planner call order.
// Dispatch reuses these charges rather than spending the allowance again.
func chargeRecallCalls(base *workflowPlanInput, calls []planner.ToolRequest) {
	if base == nil || !base.recallAvailable {
		return
	}
	if base.recallCharges == nil {
		base.recallCharges = map[string]recallCharge{}
	}
	for _, call := range calls {
		if call.Name != tools.RecallContext {
			continue
		}
		if _, charged := base.recallCharges[call.ToolCallID]; charged {
			continue
		}
		parsed, err := recall.ParseInput(call.Payload)
		if err != nil {
			// ParseInput intentionally preserves a recognized malformed read kind.
			parsed.Read, parsed.List, parsed.Find = nil, nil, nil
		}
		read := parsed.Kind == recall.Read
		accepted := base.compactionState.Recall.Charge(read)
		other := base.compactionState.Recall.Reads
		if read {
			other = base.compactionState.Recall.Discovery
		}
		base.recallCharges[call.ToolCallID] = recallCharge{kind: parsed.Kind, exhausted: !accepted, otherRemaining: other, remaining: base.compactionState.Recall}
	}
}
