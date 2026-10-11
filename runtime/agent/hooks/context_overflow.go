package hooks

import (
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// ContextOverflowEvent adopts an entire marked planner attempt in place of its
// planner_step. Recomputed records the pure recovery intent, before the next
// activity stores any new receipt-only checkpoint.
type ContextOverflowEvent struct {
	PlannerStepEvent
	Source     string                         `json:"source"`
	Call       runstore.CallKey               `json:"call"`
	Calls      []runstore.CallKey             `json:"calls"`
	Outcome    string                         `json:"outcome"`
	Failure    contextmeasure.ContextOverflow `json:"failure"`
	Recomputed *compaction.Recovery           `json:"recomputed,omitzero"`
}

// NewContextOverflowEvent reuses the complete stamped planner adoption envelope.
func NewContextOverflowEvent(step *PlannerStepEvent, failure contextmeasure.ContextOverflow, outcome string, recovery *compaction.Recovery) *ContextOverflowEvent {
	e := &ContextOverflowEvent{PlannerStepEvent: *step, Source: failure.Source, Call: failure.Failed.Call, Calls: step.CallKeys, Outcome: outcome, Failure: failure, Recomputed: recovery}
	e.SetEventKey(fmt.Sprintf("context_overflow:%s:%d", step.RunID(), step.Step))
	return e
}

// Type identifies the canonical overflow adoption record.
func (e *ContextOverflowEvent) Type() EventType {
	return ContextOverflow
}
