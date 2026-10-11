package eval

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// decodeStoredMessage checks the store boundary before restoring exact numbers.
// Model's general message decoder accepts extra members for provider compatibility.
func decodeStoredMessage(data []byte, m **model.Message) error {
	var raw struct {
		Role  jsontext.Value
		Parts []jsontext.Value
		Meta  jsontext.Value
	}
	if err := json.Unmarshal(data, &raw, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	value, err := callrecord.DecodeMessage(data)
	if err != nil {
		return err
	}
	if value != nil {
		for i, part := range value.Parts {
			if err := validateStoredPart(raw.Parts[i], part); err != nil {
				return fmt.Errorf("Parts[%d]: %w", i, err)
			}
		}
	}
	*m = value
	return nil
}

// validateStoredPart treats Kind as the discriminator and dynamic values as open JSON.
func validateStoredPart(data []byte, part model.Part) error {
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	delete(fields, "Kind")
	encoded, err := callrecord.Encode(fields)
	if err != nil {
		return err
	}
	var target any
	switch value := part.(type) {
	case model.TextPart:
		target = &value
	case model.ThinkingPart:
		target = &value
	case model.ImagePart:
		target = &value
	case model.DocumentPart:
		target = &value
	case model.CitationsPart:
		target = &value
	case model.ToolUsePart:
		target = &value
	case model.ToolResultPart:
		target = &value
	case model.CacheCheckpointPart:
		target = &value
	default:
		return fmt.Errorf("unsupported stored message part %T", part)
	}
	return json.Unmarshal(encoded, target, json.RejectUnknownMembers(true))
}
