package runtime

// workflow_state.go defines the mutable state threaded through the workflow plan loop.
//
// Contract:
// - The workflow loop has a small set of values that evolve over time (caps, attempt,
//   aggregated usage, transcript/ledger, and the current planner result).
// - Helpers mutate this state in place to keep function signatures compact and
//   to make state transitions explicit at call sites.

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

type (
	runLoopState struct {
		// Caps is the current runtime policy cap state (remaining tool budget, failure budget, etc.).
		Caps policy.CapsState

		// NextAttempt is the attempt number to stamp on the next planner activity request.
		NextAttempt int

		// AggUsage is the aggregated token usage across plan/resume iterations and tool turns.
		AggUsage model.TokenUsage

		// Result is the current planner result being processed by the loop.
		Result *planner.PlanResult

		TranscriptRef runstore.Hash
		// ToolEvents are the accumulated tool results emitted over the lifetime of this run.
		ToolEvents []*planner.ToolResult

		// ToolPolicy is the canonical model-visible and execution-visible allowlist
		// for the current planner turn.
		ToolPolicy toolPolicyEnvelope

		// turnTranscriptRecorded reports whether the streamed transcript for the
		// current planner turn has already been appended to the provider
		// conversation via recordAssistantTurn.
		turnTranscriptRecorded bool
	}
)

func newRunLoopState(result *planner.PlanResult, transcriptRef runstore.Hash, usage model.TokenUsage, caps policy.CapsState, nextAttempt int, toolPolicy toolPolicyEnvelope) *runLoopState {
	return &runLoopState{
		Caps:          caps,
		NextAttempt:   nextAttempt,
		AggUsage:      usage,
		Result:        result,
		TranscriptRef: transcriptRef,
		ToolPolicy:    cloneToolPolicyEnvelope(toolPolicy),
	}
}

// setTurnTranscript installs the streamed provider transcript for a new
// planner turn and re-arms the once-per-turn recording gate.
func (st *runLoopState) setTurnTranscript(ref runstore.Hash) {
	st.TranscriptRef = ref
	st.turnTranscriptRecorded = false
}

// takeTurnTranscript returns the streamed transcript for the current planner
// turn exactly once. Subsequent calls within the same turn return "" so a
// turn that records multiple tool_use batches (immediate execution,
// confirmations, await handshakes) never duplicates the assistant
// thinking/text in the provider conversation.
func (st *runLoopState) takeTurnTranscript() runstore.Hash {
	if st.turnTranscriptRecorded {
		return ""
	}
	st.turnTranscriptRecorded = true
	return st.TranscriptRef
}
