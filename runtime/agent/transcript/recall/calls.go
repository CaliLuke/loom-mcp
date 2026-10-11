package recall

import (
	"context"
	"encoding/json/v2"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type eligibleCall struct {
	Name       string
	ToolCallID string
	Payload    rawjson.Message
}

// calls includes canonical runtime calls and model-visible seeded tool uses.
// Both encodings are eligible content; neither follows server-only metadata.
func (r *callReader) calls(ctx context.Context, scope string, entry index.MessageEntry) ([]eligibleCall, error) {
	calls := make([]eligibleCall, 0, len(entry.Calls))
	for _, hash := range entry.Calls {
		call, err := r.call(ctx, scope, hash)
		if err != nil {
			return nil, err
		}
		calls = append(calls, call)
	}
	if entry.Kind != index.Message || entry.Role != model.ConversationRoleAssistant {
		return calls, nil
	}
	message, err := r.message(ctx, scope, entry)
	if err != nil {
		return nil, err
	}
	for _, part := range message.Parts {
		if part, ok := part.(model.ToolUsePart); ok {
			payload, err := json.Marshal(part.Input)
			if err != nil {
				return nil, err
			}
			calls = append(calls, eligibleCall{Name: part.Name, ToolCallID: part.ID, Payload: payload})
		}
	}
	return calls, nil
}

func (r *callReader) message(ctx context.Context, scope string, entry index.MessageEntry) (*model.Message, error) {
	data, err := r.handler.Store.Get(ctx, scope, entry.Hash)
	if err != nil {
		return nil, err
	}
	var message *model.Message
	if err := json.Unmarshal(data, &message); err != nil {
		return nil, err
	}
	if message == nil {
		return &model.Message{}, nil
	}
	return message, nil
}

// seededResult addresses the individual result in a caller-supplied message.
func (r *callReader) seededResult(ctx context.Context, scope, id string, entry index.MessageEntry) (eligibleText, bool, error) {
	if entry.Kind != index.Message || entry.Role != model.ConversationRoleUser {
		return eligibleText{}, false, nil
	}
	message, err := r.message(ctx, scope, entry)
	if err != nil {
		return eligibleText{}, false, err
	}
	for _, part := range message.Parts {
		if part, ok := part.(model.ToolResultPart); ok && part.ToolUseID == id {
			data, err := json.Marshal(part.Content)
			return eligibleText{text: string(data)}, true, err
		}
	}
	return eligibleText{}, false, nil
}
