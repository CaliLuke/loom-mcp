package hooks

import (
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
)

// ContextResidencyEvent records an actual prepared residency decision independently
// of its interceptable diagnostic. Coverage is verified against the next committed view.
type ContextResidencyEvent struct {
	RootRunID string `json:"root_run_id"`
	Step      int    `json:"step"`
	Coverage  string `json:"coverage"`
	Support   string `json:"support"`
	Reason    string `json:"reason"`
	Model     string `json:"model"`
	baseEvent `json:"-"`
}

// ContextResidencyAdopted identifies protected adoption of sticky attachment residency.
const ContextResidencyAdopted EventType = "context_residency_adopted"

// NewContextResidencyEvent binds a prepared decision to its recorded evidence.
func NewContextResidencyEvent(rootRunID, runID string, agentID agent.Ident, sessionID string, step int, coverage, support, reason, modelID string, timestamp time.Time) *ContextResidencyEvent {
	return &ContextResidencyEvent{RootRunID: rootRunID, Step: step, Coverage: coverage, Support: support, Reason: reason, Model: modelID, baseEvent: baseEvent{runID: runID, agentID: agentID, sessionID: sessionID, timestamp: timestamp.UnixMilli(), eventKey: fmt.Sprintf("%s:%s:%d", ContextResidencyAdopted, runID, step)}}
}

// Type identifies the protected residency adoption.
func (*ContextResidencyEvent) Type() EventType {
	return ContextResidencyAdopted
}
