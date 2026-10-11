package callrecord

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
)

// DecodeResponse restores recorded response content through the exact message
// decoder. Dynamic metadata and tool inputs retain numeric precision, and tool
// call payloads retain their original bytes. A JSON null response returns nil.
func DecodeResponse(data []byte) (*model.Response, error) {
	var response *model.Response
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode recorded response: %w", err)
	}
	if response == nil {
		return nil, nil
	}
	var fields struct {
		Content   []jsontext.Value
		ToolCalls []struct{ Payload jsontext.Value }
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("decode recorded response content: %w", err)
	}
	response.Content = nil
	if fields.Content != nil {
		response.Content = make([]model.Message, 0, len(fields.Content))
	}
	for _, data := range fields.Content {
		message, err := decodeMessage(data)
		if err != nil {
			return nil, err
		}
		if message == nil {
			return nil, fmt.Errorf("decode recorded response: null content message")
		}
		response.Content = append(response.Content, *message)
	}
	for i, call := range fields.ToolCalls {
		response.ToolCalls[i].Payload = rawjson.Message(call.Payload)
	}
	return response, nil
}
