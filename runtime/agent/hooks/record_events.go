package hooks

import (
	"fmt"
	"slices"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type (
	// PlannerStepEvent adopts every started call returned by one planner activity.
	// ViewRef names the committed view used by that activity before history policy.
	PlannerStepEvent struct {
		// ContextOverrides records explicit per-run settings for budget validation.
		ContextOverrides bool `json:"context_overrides,omitzero"`
		// ContextFingerprint identifies the stored canonical effective settings.
		ContextFingerprint string `json:"context_fingerprint,omitzero"`
		// ContextSettings is resolved for every agent, including threshold opt-outs.
		ContextSettings *compaction.ContextSettings `json:"context_settings,omitzero"`
		// CompactionSettings is the client-stamped configuration, repeated on canonical adoption records.
		CompactionSettings *compaction.Settings `json:"compaction_settings,omitzero"`
		// RecoverySettings records resolved lookup settings independently of compaction.
		RecoverySettings *run.ContextRecovery `json:"recovery_settings,omitzero"`
		// KeepResultTools preserves the run configuration independently of worker options.
		KeepResultTools []tools.Ident `json:"keep_result_tools,omitzero"`
		// CheckpointBytes is the adopted rendered size, qualified by ViewRef.
		CheckpointBytes int `json:"checkpoint_bytes,omitzero"`
		// Recovery marks an output rejected by the model-recovery path. Its view
		// names the calls' evidence and is not adopted as the next stable view.
		Recovery bool `json:"recovery,omitzero"`
		// Recovering marks an activity scheduled with correction guidance, which
		// preserves the stable view instead of applying threshold transformations.
		Recovering bool `json:"recovering,omitzero"`
		baseEvent
		// ProtocolVersion is the workflow input version validated before execution.
		ProtocolVersion int
		// Step is the one-based planner activity schedule number.
		Step int
		// IndexHead is the head returned after committing the activity's pending entries.
		IndexHead runstore.Hash
		// ViewRef identifies the committed view before history policy.
		ViewRef index.ViewRef
		// CallKeys contains every invocation started by the returned attempt.
		CallKeys []runstore.CallKey
	}

	// RunSealedEvent names the terminal transcript after pending entries commit.
	RunSealedEvent struct {
		// ContextFingerprint identifies the stored canonical effective settings.
		ContextFingerprint string                      `json:"context_fingerprint,omitzero"`
		ContextSettings    *compaction.ContextSettings `json:"context_settings,omitzero"`
		// CompactionSettings and KeepResultTools are the stamped run configuration.
		CompactionSettings *compaction.Settings `json:"compaction_settings,omitzero"`
		// RecoverySettings records resolved lookup settings independently of compaction.
		RecoverySettings *run.ContextRecovery `json:"recovery_settings,omitzero"`
		KeepResultTools  []tools.Ident        `json:"keep_result_tools,omitzero"`
		baseEvent
		// ProtocolVersion is the workflow input version validated before execution.
		ProtocolVersion int
		// IndexHead addresses the complete sealed transcript.
		IndexHead runstore.Hash
	}

	// ContextRequestGrowthEvent reports the first request in a run whose message
	// bytes outside the loaded view exceed those inside it.
	ContextRequestGrowthEvent struct {
		baseEvent
		// Key identifies the first request dominated by outside-view bytes.
		Key runstore.CallKey
		// Inside counts encoded message bytes matched to the recorded view.
		Inside int
		// Outside counts encoded message bytes supplied independently of the view.
		Outside int
	}
)

const (
	// PlannerStep is the canonical planner activity adoption boundary.
	PlannerStep EventType = "planner_step"
	// RunSealed identifies the canonical terminal index boundary.
	RunSealed EventType = "run_sealed"
	// ContextCompacted reserves a canonical compaction boundary.
	ContextCompacted EventType = "context_compacted"
	// ContextCompactionSkipped reserves the canonical skipped-compaction record.
	ContextCompactionSkipped EventType = "context_compaction_skipped"
	// ContextOverflow identifies the canonical overflow adoption record.
	ContextOverflow EventType = "context_overflow"
	// ContextRequestGrowth identifies requests dominated by planner-owned history.
	ContextRequestGrowth EventType = "context_request_growth"
)

// IsRecordEvent reports whether kind belongs to the runtime's uninterceptable
// commit protocol. The reserved kinds acquire producers in later phases.
func IsRecordEvent(kind EventType) bool {
	return kind == PlannerStep || kind == RunSealed || kind == ContextCompacted ||
		kind == ContextCompactionSkipped || kind == ContextOverflow || kind == ChildRunLinked || kind == ContextRecalled
}

// NewPlannerStepEvent constructs a replay-stable adoption record with workflow time.
func NewPlannerStepEvent(runID string, agentID agent.Ident, sessionID string, step int, view index.ViewRef, keys []runstore.CallKey, protocolVersion int, timestamp time.Time) *PlannerStepEvent {
	base := newBaseEvent(runID, agentID)
	base.sessionID = sessionID
	base.SetEventKey(fmt.Sprintf("planner_step:%s:%d", runID, step))
	base.SetTimestampMS(timestamp.UnixMilli())
	return &PlannerStepEvent{baseEvent: base, ProtocolVersion: protocolVersion, Step: step, IndexHead: view.IndexHead, ViewRef: view, CallKeys: slices.Clone(keys)}
}

// NewRunSealedEvent constructs the fixed logical seal identity with workflow time.
func NewRunSealedEvent(runID string, head runstore.Hash, protocolVersion int, timestamp time.Time) *RunSealedEvent {
	return &RunSealedEvent{baseEvent: baseEvent{runID: runID, eventKey: "run_sealed:" + runID, timestamp: timestamp.UnixMilli()}, ProtocolVersion: protocolVersion, IndexHead: head}
}

// NewContextRequestGrowthEvent constructs a run-scoped growth diagnostic.
func NewContextRequestGrowthEvent(runID string, agentID agent.Ident, sessionID string, key runstore.CallKey, inside, outside int) *ContextRequestGrowthEvent {
	base := newBaseEvent(runID, agentID)
	base.sessionID = sessionID
	return &ContextRequestGrowthEvent{baseEvent: base, Key: key, Inside: inside, Outside: outside}
}

// Type identifies the canonical adoption event.
func (e *PlannerStepEvent) Type() EventType {
	return PlannerStep
}

// Type identifies the terminal transcript seal.
func (e *RunSealedEvent) Type() EventType {
	return RunSealed
}

// Type identifies the request growth diagnostic.
func (e *ContextRequestGrowthEvent) Type() EventType {
	return ContextRequestGrowth
}

func decodeRecordEvent(input *ActivityInput) (Event, bool, error) {
	if event, ok, err := decodeRecallEvent(input); ok || err != nil {
		return event, ok, err
	}
	if event, ok, err := decodeContextEvent(input); ok || err != nil {
		return event, ok, err
	}
	if input.Type == PlannerStep {
		var p PlannerStepEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		p.baseEvent = newBaseEvent(input.RunID, input.AgentID)
		p.sessionID = input.SessionID
		return &p, true, nil
	}
	if input.Type == RunSealed {
		var p RunSealedEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		p.baseEvent = baseEvent{runID: input.RunID, agentID: input.AgentID, sessionID: input.SessionID}
		return &p, true, nil
	}
	if input.Type == ContextRequestGrowth {
		var p ContextRequestGrowthEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		p.baseEvent = newBaseEvent(input.RunID, input.AgentID)
		p.sessionID = input.SessionID
		return &p, true, nil
	}
	return nil, false, nil
}

func decodeRecallEvent(input *ActivityInput) (Event, bool, error) {
	if input.Type == ContextRecalled {
		var event ContextRecalledEvent
		if err := decodeHookPayload(input, &event); err != nil {
			return nil, false, err
		}
		event.baseEvent = baseEvent{runID: input.RunID, agentID: input.AgentID, sessionID: input.SessionID}
		return &event, true, nil
	}
	if input.Type == RecallBudgetExhausted {
		var event RecallBudgetExhaustedEvent
		if err := decodeHookPayload(input, &event); err != nil {
			return nil, false, err
		}
		event.baseEvent = baseEvent{runID: input.RunID, agentID: input.AgentID, sessionID: input.SessionID}
		return &event, true, nil
	}

	return nil, false, nil
}

func decodeContextEvent(input *ActivityInput) (Event, bool, error) {
	if input.Type == ContextOverflow {
		var event ContextOverflowEvent
		if err := decodeHookPayload(input, &event); err != nil {
			return nil, false, err
		}
		event.baseEvent = baseEvent{runID: input.RunID, agentID: input.AgentID, sessionID: input.SessionID}
		return &event, true, nil
	}
	if input.Type == ContextCompacted || input.Type == ContextCompactionSkipped {
		var p ContextCompactionEvent
		if err := decodeHookPayload(input, &p); err != nil {
			return nil, false, err
		}
		p.baseEvent = baseEvent{runID: input.RunID, agentID: input.AgentID, sessionID: input.SessionID}
		p.kind = input.Type
		return &p, true, nil
	}
	if input.Type == ContextUnmeasured || input.Type == ContextLimitsUnknown || input.Type == ContextBudgetTooSmall || input.Type == ContextRecallDisabled {
		return &ContextDiagnosticEvent{baseEvent: baseEvent{runID: input.RunID, agentID: input.AgentID, sessionID: input.SessionID}, kind: input.Type}, true, nil
	}

	return nil, false, nil
}
