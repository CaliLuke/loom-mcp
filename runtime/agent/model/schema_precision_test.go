package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
)

func TestRequestContractPreservesIntegerBounds(t *testing.T) {
	for _, tt := range []struct {
		name    string
		schema  string
		valid   []string
		invalid []string
	}{
		{
			name:    "signed",
			schema:  `{"type":"object","required":["value"],"properties":{"value":{"type":"integer","minimum":-9223372036854775808,"maximum":9223372036854775807}}}`,
			valid:   []string{"-9223372036854775808", "9223372036854775807", "0"},
			invalid: []string{"-9223372036854775809", "9223372036854775808", "9223372036854775807.5"},
		},
		{
			name:    "unsigned",
			schema:  `{"type":"object","required":["value"],"properties":{"value":{"type":"integer","minimum":0,"maximum":18446744073709551615}}}`,
			valid:   []string{"0", "18446744073709551615"},
			invalid: []string{"-1", "18446744073709551616", "18446744073709551615.5"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			contract, err := NewRequestContract(&Request{Tools: []*ToolDefinition{{Name: "number", InputSchema: rawjson.Message(tt.schema)}}})
			require.NoError(t, err)
			structured, err := NewRequestContract(&Request{StructuredOutput: &StructuredOutput{Name: "number", Schema: rawjson.Message(tt.schema)}})
			require.NoError(t, err)
			for _, value := range tt.valid {
				payload := `{"value":` + value + `}`
				assert.NoError(t, contract.validateToolCallPayloads([]ToolCall{{Name: "number", Payload: rawjson.Message(payload)}}), value)
				assert.NoError(t, structured.validateStructuredResponse(&Response{Content: []Message{{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: payload}}}}}), value)
			}
			for _, value := range tt.invalid {
				payload := `{"value":` + value + `}`
				require.Error(t, contract.validateToolCallPayloads([]ToolCall{{Name: "number", Payload: rawjson.Message(payload)}}), value)
				assert.Error(t, structured.validateStructuredResponse(&Response{Content: []Message{{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: payload}}}}}), value)
			}
		})
	}
}

func TestDecodeSchemaJSONRetainsStrictValidation(t *testing.T) {
	for _, raw := range []string{`{"value":1,"value":2}`, `{"value":1} {}`, "{\"value\":\"\xff\"}", `{"value":`} {
		_, err := decodeSchemaJSON([]byte(raw))
		assert.Error(t, err)
	}
}
