package runtime

// workflow_transcript.go records assistant transcript and tool_use declarations into the
// conversation message list that is fed back into the planner.
//
// Contract:
// - Produces a canonical assistant message that includes all tool_use parts for the turn.
// - Appends messages to the PlanInput in the same order used for tool_result correlation.

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

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
