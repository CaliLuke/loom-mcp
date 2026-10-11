package hooks

import (
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

type (
	// ContextCompactionEvent records a committed compaction or a suppressed attempt.
	ContextCompactionEvent struct {
		baseEvent  `json:"-"`
		kind       EventType
		Reason     string             `json:"reason"`
		Tier       string             `json:"tier,omitzero"`
		Units      int                `json:"units"`
		Calls      int                `json:"calls"`
		Before     int                `json:"before"`
		After      int                `json:"after"`
		Checkpoint *runstore.Hash     `json:"checkpoint,omitzero"`
		Step       int                `json:"step"`
		CallKeys   []runstore.CallKey `json:"call_keys"`
	}
	// ContextDiagnosticEvent reports a run-scoped context measurement limitation.
	ContextDiagnosticEvent struct {
		baseEvent `json:"-"`
		kind      EventType
	}
)

const (
	// ContextUnmeasured reports an adopted attempt with no view-bearing requests.
	ContextUnmeasured EventType = "context_unmeasured"
	// ContextLimitsUnknown reports missing limits for a view-bearing request.
	ContextLimitsUnknown EventType = "context_limits_unknown"
	// ContextBudgetTooSmall reports sticky threshold disablement below 2900 tokens.
	ContextBudgetTooSmall EventType = "context_budget_too_small"
)

// NewContextCompactionEvent constructs a canonical record with workflow time and stable identity.
func NewContextCompactionEvent(runID string, agentID agent.Ident, sessionID string, step int, kind EventType, reason, tier string, units, calls, before, after int, checkpoint *runstore.Hash, timestamp time.Time) *ContextCompactionEvent {
	base := baseEvent{runID: runID, agentID: agentID, sessionID: sessionID, timestamp: timestamp.UnixMilli(), eventKey: fmt.Sprintf("%s:%s:%d", kind, runID, step)}
	return &ContextCompactionEvent{baseEvent: base, kind: kind, Reason: reason, Tier: tier, Units: units, Calls: calls, Before: before, After: after, Checkpoint: checkpoint, Step: step, CallKeys: []runstore.CallKey{}}
}

// NewContextDiagnosticEvent constructs a normal hook with once-per-run identity.
func NewContextDiagnosticEvent(runID string, agentID agent.Ident, sessionID string, kind EventType, timestamp time.Time) *ContextDiagnosticEvent {
	return &ContextDiagnosticEvent{baseEvent: baseEvent{runID: runID, agentID: agentID, sessionID: sessionID, timestamp: timestamp.UnixMilli(), eventKey: fmt.Sprintf("%s:%s", kind, runID)}, kind: kind}
}

// Type identifies the canonical compaction record.
func (e *ContextCompactionEvent) Type() EventType { return e.kind }

// Type identifies the run-scoped diagnostic.
func (e *ContextDiagnosticEvent) Type() EventType { return e.kind }
