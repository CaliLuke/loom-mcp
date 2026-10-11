package hooks

import (
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/recall"
)

type (
	// ContextRecalledEvent records one charged context lookup with stable call identity.
	ContextRecalledEvent struct {
		baseEvent          `json:"-"`
		Kind               recall.Kind `json:"kind"`
		Outcome            string      `json:"outcome"`
		Code               recall.Code `json:"code,omitzero"`
		ToolUseID          string      `json:"tool_use_id"`
		Bytes              int         `json:"bytes"`
		RemainingDiscovery int         `json:"remaining_discovery"`
		RemainingReads     int         `json:"remaining_reads"`
		Step               int         `json:"step"`
	}
	// RecallBudgetExhaustedEvent reports the first exhausted call in one allowance episode.
	RecallBudgetExhaustedEvent struct {
		baseEvent `json:"-"`
		Allowance string `json:"allowance"`
	}
)

const (
	// ContextRecalled identifies a canonical context lookup charge and outcome.
	ContextRecalled EventType = "context_recalled"
	// ContextRecallDisabled identifies the first shortened view of a disabled run.
	ContextRecallDisabled EventType = "context_recall_disabled"
	// RecallBudgetExhausted identifies one exhausted allowance episode.
	RecallBudgetExhausted EventType = "recall_budget_exhausted"
)

// NewContextRecalledEvent constructs a workflow-time lookup record.
func NewContextRecalledEvent(runID string, agentID agent.Ident, sessionID, toolUseID string, timestamp time.Time) *ContextRecalledEvent {
	return &ContextRecalledEvent{baseEvent: baseEvent{runID: runID, agentID: agentID, sessionID: sessionID, eventKey: "context_recalled:" + runID + ":" + toolUseID, timestamp: timestamp.UnixMilli()}, ToolUseID: toolUseID}
}

// NewRecallBudgetExhaustedEvent constructs an allowance episode diagnostic.
func NewRecallBudgetExhaustedEvent(runID string, agentID agent.Ident, sessionID, allowance string, episode int, timestamp time.Time) *RecallBudgetExhaustedEvent {
	return &RecallBudgetExhaustedEvent{baseEvent: baseEvent{runID: runID, agentID: agentID, sessionID: sessionID, eventKey: fmt.Sprintf("recall_budget_exhausted:%s:%s:%d", runID, allowance, episode), timestamp: timestamp.UnixMilli()}, Allowance: allowance}
}

// Type identifies the canonical lookup event.
func (e *ContextRecalledEvent) Type() EventType {
	return ContextRecalled
}

// Type identifies an exhaustion episode.
func (e *RecallBudgetExhaustedEvent) Type() EventType {
	return RecallBudgetExhausted
}
