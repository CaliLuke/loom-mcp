package runtime

// workflow_state.go defines the mutable state threaded through the workflow plan loop.
//
// Contract:
// - The workflow loop has a small set of values that evolve over time (caps, attempt,
//   aggregated usage, transcript/ledger, and the current planner result).
// - Helpers mutate this state in place to keep function signatures compact and
//   to make state transitions explicit at call sites.

import (
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/runtime/agent/transcript"
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

		// Transcript is the provider transcript for the current planner result.
		Transcript []*model.Message

		// Ledger is the provider transcript ledger used to merge tool_use/tool_result into messages.
		Ledger *transcript.Ledger

		// ToolEvents are the accumulated tool results emitted over the lifetime of this run.
		ToolEvents []*planner.ToolResult

		// ToolOutputs is the accumulated executed tool-call history emitted over
		// the lifetime of this run.
		ToolOutputs []*planner.ToolOutput

		// TypedInputs is the accumulated typed human-input history emitted over
		// the lifetime of this run.
		TypedInputs []planner.TypedInputOutput

		// ToolPolicy is the canonical model-visible and execution-visible allowlist
		// for the current planner turn.
		ToolPolicy toolPolicyEnvelope

		// turnTranscriptRecorded reports whether the streamed transcript for the
		// current planner turn has already been appended to the provider
		// conversation via recordAssistantTurn.
		turnTranscriptRecorded bool
	}
)

func newRunLoopState(result *planner.PlanResult, transcriptMsgs []*model.Message, usage model.TokenUsage, caps policy.CapsState, nextAttempt int, toolPolicy toolPolicyEnvelope) *runLoopState {
	return &runLoopState{
		Caps:        caps,
		NextAttempt: nextAttempt,
		AggUsage:    usage,
		Result:      result,
		Transcript:  transcriptMsgs,
		Ledger:      transcript.FromModelMessages(transcriptMsgs),
		ToolPolicy:  cloneToolPolicyEnvelope(toolPolicy),
	}
}

// setTurnTranscript installs the streamed provider transcript for a new
// planner turn and re-arms the once-per-turn recording gate.
func (st *runLoopState) setTurnTranscript(msgs []*model.Message) {
	st.Transcript = msgs
	st.turnTranscriptRecorded = false
}

// takeTurnTranscript returns the streamed transcript for the current planner
// turn exactly once. Subsequent calls within the same turn return nil so a
// turn that records multiple tool_use batches (immediate execution,
// confirmations, await handshakes) never duplicates the assistant
// thinking/text in the provider conversation.
func (st *runLoopState) takeTurnTranscript() []*model.Message {
	if st.turnTranscriptRecorded {
		return nil
	}
	st.turnTranscriptRecorded = true
	return st.Transcript
}
