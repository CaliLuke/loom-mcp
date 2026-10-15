package model

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStructuredOutputRejectsAmbiguousOrMalformedContent(t *testing.T) {
	request := &Request{StructuredOutput: &StructuredOutput{Name: "result", Schema: rawjson.Message(`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`)}}
	contract, err := NewRequestContract(request)
	require.NoError(t, err)
	for _, tc := range []struct {
		name    string
		content []Message
	}{
		{"malformed", []Message{{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "{"}}}}},
		{"empty text", []Message{{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: " "}}}}},
		{"thinking only", []Message{{Role: ConversationRoleAssistant, Parts: []Part{ThinkingPart{Text: "thinking", Signature: "signed"}}}}},
		{"multiple messages", []Message{{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: `{"answer":"yes"}`}}}, {Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: `{"answer":"no"}`}}}}},
		{"multiple citations", []Message{{Role: ConversationRoleAssistant, Parts: []Part{CitationsPart{Text: `{"answer":"yes"}`}, CitationsPart{Text: `{"answer":"no"}`}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accepted, err := contract.ValidateResponse(&Response{Content: tc.content, StopReason: "end_turn"})
			var failure *OutputValidationError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, OutputValidationStructuredOutput, failure.Kind())
			assert.Nil(t, accepted)
		})
	}
}

func TestCompletionValidatorRejectsMissingContract(t *testing.T) {
	validate := func(*Response, *Completion) error {
		return nil
	}
	require.ErrorContains(t, SetCompletionValidator(nil, validate), "request")
	require.ErrorContains(t, SetCompletionValidator(&Request{}, validate), "structured output")
	require.ErrorContains(t, SetCompletionValidator(&Request{StructuredOutput: &StructuredOutput{}}, nil), "required")
}
