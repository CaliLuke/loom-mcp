package runtime

// workflow_transcript.go records assistant transcript and tool_use declarations into the
// conversation message list that is fed back into the planner.
//
// Contract:
// - Produces a canonical assistant message that includes all tool_use parts for the turn.
// - Appends messages to the PlanInput in the same order used for tool_result correlation.

import (
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
)

// recordAssistantTurn merges streamed transcript parts with the declared tool calls
// and appends the resulting assistant messages to the conversation state.
//
// Callers must source transcriptMsgs from runLoopState.takeTurnTranscript so the
// streamed thinking/text of a planner turn is appended exactly once even when the
// turn records multiple tool_use batches (immediate execution, confirmations, and
// await handshakes each declare their own batch).
func (r *Runtime) recordAssistantTurn(base *planner.PlanInput, transcriptMsgs []*model.Message, allowed []planner.ToolRequest, led *transcript.Ledger) {
	if led == nil {
		led = transcript.NewLedger()
	}
	if len(transcriptMsgs) == 0 && len(allowed) == 0 {
		return
	}
	for _, call := range allowed {
		led.DeclareToolUse(call.ToolCallID, string(call.Name), call.Payload)
	}
	// Flush a single assistant message capturing the full turn (thinking/text
	// plus all tool_use blocks) so the next user message can correlate to the
	// complete set of tool_use IDs.
	led.FlushAssistant()
	messages := cloneMessages(transcriptMsgs)
	target := findAssistantMessage(messages)
	if target == nil {
		target = &model.Message{Role: model.ConversationRoleAssistant}
		messages = append(messages, target)
	}
	for _, call := range allowed {
		target.Parts = append(target.Parts, model.ToolUsePart{
			ID:    call.ToolCallID,
			Name:  string(call.Name),
			Input: call.Payload,
		})
	}
	base.Messages = append(base.Messages, messages...)
}

// findAssistantMessage returns the last assistant message in msgs, if any.
func findAssistantMessage(msgs []*model.Message) *model.Message {
	for _, msg := range slices.Backward(msgs) {
		if msg != nil && msg.Role == model.ConversationRoleAssistant {
			return msg
		}
	}
	return nil
}

// cloneMessages shallow-copies messages and their parts so callers can mutate
// assistant parts without mutating the original transcript slice.
func cloneMessages(msgs []*model.Message) []*model.Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]*model.Message, 0, len(msgs))
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		parts := make([]model.Part, len(msg.Parts))
		copy(parts, msg.Parts)
		out = append(out, &model.Message{
			Role:  msg.Role,
			Parts: parts,
			Meta:  cloneMetadata(msg.Meta),
		})
	}
	return out
}
