package transcript

import (
	"fmt"
	"maps"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

type (
	// Grammar specifies the provider's role and thinking requirements.
	Grammar interface {
		viewRules() (alternating, thinking bool)
	}
	// GrammarBedrock requires alternating roles and optionally thinking before tool use.
	GrammarBedrock struct{ Thinking bool }
	// GrammarAnthropic accepts adjacent like roles but requires complete tool handshakes.
	GrammarAnthropic struct{}
)

const (
	// CheckpointMetaKey identifies a framework checkpoint to the pure view validator.
	CheckpointMetaKey = "context_checkpoint"
)

// ValidateView validates the loaded conversation, ignoring system messages.
func ValidateView(messages []*model.Message, grammar Grammar) error {
	alternating, thinking := grammar.viewRules()
	var previous model.ConversationRole
	pending := map[string]bool{}
	checkpointNext := false
	for i, message := range messages {
		if message == nil {
			return fmt.Errorf("view message %d is nil", i)
		}
		if message.Role == model.ConversationRoleSystem {
			continue
		}
		if message.Role != model.ConversationRoleUser && message.Role != model.ConversationRoleAssistant {
			return fmt.Errorf("view message %d has invalid role %q", i, message.Role)
		}
		if previous == "" && message.Role != model.ConversationRoleUser {
			return fmt.Errorf("view must start with a user message")
		}
		if checkpointNext && message.Role != model.ConversationRoleAssistant {
			return fmt.Errorf("checkpoint tail must start with an assistant message")
		}
		if alternating && previous == message.Role {
			return fmt.Errorf("view message %d repeats role %q", i, message.Role)
		}
		calls, results, err := viewHandshakeParts(message, thinking)
		if err != nil {
			return fmt.Errorf("view message %d: %w", i, err)
		}
		if len(pending) > 0 || len(results) > 0 {
			if !maps.Equal(pending, results) {
				return fmt.Errorf("view message %d: calls and results do not match", i)
			}
		}
		pending = calls
		checkpointNext = message.Meta[CheckpointMetaKey] == true
		previous = message.Role
	}
	if len(pending) > 0 {
		return fmt.Errorf("view ends with unanswered tool calls")
	}
	return nil
}

// AssembleView strips reasoning before the tail and retains the tail intact.
func AssembleView(system []*model.Message, checkpoint *model.Message, tail []*model.Message) []*model.Message {
	messages := make([]*model.Message, 0, len(system)+len(tail)+1)
	for _, message := range system {
		messages = append(messages, withoutReasoning(message))
	}
	if checkpoint != nil {
		message := withoutReasoning(checkpoint)
		if message.Meta == nil {
			message.Meta = map[string]any{}
		}
		message.Meta[CheckpointMetaKey] = true
		messages = append(messages, message)
	}
	return append(messages, tail...)
}

// withoutReasoning owns the parts and metadata containers before filtering them.
func withoutReasoning(message *model.Message) *model.Message {
	copyMessage := *message
	copyMessage.Meta = maps.Clone(message.Meta)
	copyMessage.Parts = nil
	for _, part := range message.Parts {
		switch part.(type) {
		case model.ThinkingPart, *model.ThinkingPart:
			continue
		default:
			copyMessage.Parts = append(copyMessage.Parts, part)
		}
	}
	return &copyMessage
}

// viewHandshakeParts rejects duplicate IDs and parts on the wrong side of a handshake.
func viewHandshakeParts(message *model.Message, thinking bool) (map[string]bool, map[string]bool, error) {
	calls, results := map[string]bool{}, map[string]bool{}
	for _, part := range message.Parts {
		switch value := handshakePart(part).(type) {
		case model.ToolUsePart:
			if message.Role != model.ConversationRoleAssistant || value.ID == "" || calls[value.ID] {
				return nil, nil, fmt.Errorf("invalid or duplicate tool call %q", value.ID)
			}
			calls[value.ID] = true
		case model.ToolResultPart:
			if message.Role != model.ConversationRoleUser || value.ToolUseID == "" || results[value.ToolUseID] {
				return nil, nil, fmt.Errorf("invalid or duplicate tool result %q", value.ToolUseID)
			}
			results[value.ToolUseID] = true
		}
	}
	if thinking && len(calls) > 0 {
		if _, ok := handshakePart(message.Parts[0]).(model.ThinkingPart); !ok {
			return nil, nil, fmt.Errorf("tool use must start with intact thinking")
		}
	}
	return calls, results, nil
}

func handshakePart(part model.Part) model.Part {
	switch value := part.(type) {
	case *model.ThinkingPart:
		return *value
	case *model.ToolUsePart:
		return *value
	case *model.ToolResultPart:
		return *value
	default:
		return part
	}
}

func (g GrammarBedrock) viewRules() (bool, bool) {
	return true, g.Thinking
}

func (GrammarAnthropic) viewRules() (bool, bool) {
	return false, false
}
