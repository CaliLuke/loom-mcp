package callrecord

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// SnapshotMessages owns the exact encoded view before history policy can mutate
// it. It preserves dynamic numeric tokens without imposing model-request bounds
// on a view from which the planner may select only a small subset.
func SnapshotMessages(messages []*model.Message) ([]*model.Message, error) {
	snapshot := make([]*model.Message, 0, len(messages))
	for _, message := range messages {
		data, err := Encode(message)
		if err != nil {
			return nil, err
		}
		owned, err := decodeMessage(data)
		if err != nil {
			return nil, err
		}
		snapshot = append(snapshot, owned)
	}
	return snapshot, nil
}

// DecodeRequest restores an exported request without a store or view renderer.
// Dynamic message and schema values retain exact numeric tokens, including integers
// above 2^53. This is the decoder for self-contained RunRecord exports.
func DecodeRequest(data []byte) (*model.Request, error) {
	var request *model.Request
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, fmt.Errorf("decode exported request: %w", err)
	}
	if request == nil {
		return nil, nil
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("decode exported request fields: %w", err)
	}
	var messages []jsontext.Value
	if err := json.Unmarshal(fields["Messages"], &messages); err != nil {
		return nil, fmt.Errorf("decode exported request messages: %w", err)
	}
	request.Messages = nil
	if messages != nil {
		request.Messages = make([]*model.Message, 0, len(messages))
	}
	for _, data := range messages {
		message, err := decodeMessage(data)
		if err != nil {
			return nil, err
		}
		request.Messages = append(request.Messages, message)
	}
	tools, err := decodeTools(fields["Tools"])
	if err != nil {
		return nil, err
	}
	// Rebuild normalizes tools to an owned non-nil slice even for an empty catalog.
	request.Tools = tools
	return request, nil
}

// DecodeMessage restores a recorded message while preserving exact dynamic numeric tokens.
func DecodeMessage(data []byte) (*model.Message, error) {
	return decodeMessage(data)
}

// messageStable checks the ordinary view decoder before a message can use a span.
func messageStable(data []byte) bool {
	var message *model.Message
	if err := json.Unmarshal(data, &message); err != nil {
		return false
	}
	encoded, err := Encode(message)
	return err == nil && bytes.Equal(data, encoded)
}

// decodeMessage keeps numeric tokens in dynamic fields that model's ordinary
// decoder converts to float64. Typed parts still use model's discriminator.
func decodeMessage(data []byte) (*model.Message, error) {
	var message *model.Message
	if err := json.Unmarshal(data, &message); err != nil {
		return nil, fmt.Errorf("decode recorded message: %w", err)
	}
	if message == nil {
		return nil, nil
	}
	var raw map[string]jsontext.Value
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode recorded message fields: %w", err)
	}
	if err := restoreMetadata(message, raw["Meta"]); err != nil {
		return nil, err
	}
	if err := restorePartValues(message, raw["Parts"]); err != nil {
		return nil, err
	}

	return message, nil
}

func readTools(ctx context.Context, store runstore.Store, scope string, hash runstore.Hash) ([]*model.ToolDefinition, error) {
	data, err := store.Get(ctx, scope, hash)
	if err != nil {
		return nil, fmt.Errorf("read recorded tools: %w", err)
	}
	return decodeTools(data)
}

func decodeTools(data []byte) ([]*model.ToolDefinition, error) {
	var raw []jsontext.Value
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode recorded tools: %w", err)
	}
	tools := make([]*model.ToolDefinition, 0, len(raw))
	for _, data := range raw {
		var tool *model.ToolDefinition
		if err := json.Unmarshal(data, &tool); err != nil {
			return nil, fmt.Errorf("decode recorded tool: %w", err)
		}
		if tool != nil {
			var fields map[string]jsontext.Value
			if err := json.Unmarshal(data, &fields); err != nil {
				return nil, fmt.Errorf("decode recorded tool fields: %w", err)
			}
			schema, err := model.DecodeSchemaJSON(fields["InputSchema"])
			if err != nil {
				return nil, fmt.Errorf("decode recorded tool schema: %w", err)
			}
			tool.InputSchema = schema
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

func restoreMetadata(message *model.Message, data jsontext.Value) error {
	if len(data) > 0 && string(data) != "null" {
		meta := data
		var fields map[string]jsontext.Value
		if err := json.Unmarshal(meta, &fields); err != nil {
			return fmt.Errorf("decode recorded metadata: %w", err)
		}
		for key, value := range fields {
			decoded, err := model.DecodeSchemaJSON(value)
			if err != nil {
				return fmt.Errorf("decode recorded metadata value: %w", err)
			}
			message.Meta[key] = decoded
		}
	}
	return nil
}

func restorePartValues(message *model.Message, data jsontext.Value) error {
	var parts []map[string]jsontext.Value
	if err := json.Unmarshal(data, &parts); err != nil {
		return fmt.Errorf("decode recorded part fields: %w", err)
	}
	for position, part := range message.Parts {
		switch value := part.(type) {
		case model.ToolUsePart:
			decoded, err := model.DecodeSchemaJSON(parts[position]["Input"])
			if err != nil {
				return fmt.Errorf("decode recorded tool input: %w", err)
			}
			value.Input = decoded
			message.Parts[position] = value
		case model.ToolResultPart:
			decoded, err := model.DecodeSchemaJSON(parts[position]["Content"])
			if err != nil {
				return fmt.Errorf("decode recorded tool result: %w", err)
			}
			value.Content = decoded
			message.Parts[position] = value
		}
	}
	return nil
}
