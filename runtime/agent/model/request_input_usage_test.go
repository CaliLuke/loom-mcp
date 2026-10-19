package model

import (
	"encoding/json/v2"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestInputUsageSurvivesResponseAndRejection(t *testing.T) {
	contract, err := NewRequestContract(&Request{Messages: []*Message{{Role: ConversationRoleUser, Parts: []Part{TextPart{Text: "question"}}}}})
	require.NoError(t, err)
	response := &Response{Content: []Message{{Role: ConversationRoleAssistant, Parts: []Part{TextPart{Text: "answer"}}}}, StopReason: "STOP", Usage: TokenUsage{InputTokens: 20, CacheReadTokens: 2, OutputTokens: 4, TotalTokens: 26}, RequestInputUsage: RequestInputUsage{Present: true, InputTokens: 10}}
	owned, err := contract.ValidateResponse(response)
	require.NoError(t, err)
	response.RequestInputUsage.InputTokens = 99
	assert.Equal(t, 10, owned.RequestInputUsage.InputTokens)
	data, err := json.Marshal(owned)
	require.NoError(t, err)
	var decoded Response
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, owned.RequestInputUsage, decoded.RequestInputUsage)
	owned.OutputLimited = true
	_, err = contract.ValidateResponse(owned)
	var rejection *OutputValidationError
	require.ErrorAs(t, err, &rejection)
	assert.Equal(t, owned.RequestInputUsage, rejection.Evidence().RequestInputUsage)
	assert.Equal(t, &owned.Usage, rejection.Usage())
	restored, err := RestoreOutputValidationError(rejection.Kind(), errors.New("cause"), rejection.Evidence(), rejection.Usage())
	require.NoError(t, err)
	assert.Equal(t, owned.RequestInputUsage, restored.Evidence().RequestInputUsage)
	evidence := restored.Evidence()
	evidence.RequestInputUsage.InputTokens = 1
	assert.Equal(t, 10, restored.Evidence().RequestInputUsage.InputTokens)
}

func TestRequestInputUsageRejectsMalformedCounts(t *testing.T) {
	contract, err := NewRequestContract(&Request{Messages: []*Message{{Role: ConversationRoleUser, Parts: []Part{TextPart{Text: "question"}}}}})
	require.NoError(t, err)
	for _, input := range []RequestInputUsage{{InputTokens: 1}, {Present: true, InputTokens: -1}, {Present: true, InputTokens: 11}} {
		t.Run(stringMustJSON(t, input), func(t *testing.T) {
			response := &Response{Usage: TokenUsage{InputTokens: 8, CacheReadTokens: 2, TotalTokens: 10}, RequestInputUsage: input}
			_, err := CloneResponse(response)
			require.Error(t, err)
			_, err = contract.ValidateResponse(response)
			var rejection *OutputValidationError
			require.ErrorAs(t, err, &rejection)
			assert.Equal(t, OutputValidationUsage, rejection.Kind())
			assert.Equal(t, RequestInputUsage{Present: true}, rejection.Evidence().RequestInputUsage)
			assert.Equal(t, &response.Usage, rejection.Usage())
			_, err = RestoreOutputValidationError(OutputValidationUsage, errors.New("cause"), ResponseEvidence{RequestInputUsage: input}, &response.Usage)
			require.Error(t, err)
		})
	}
}

func TestRequestInputUsageZeroAndLegacyEncoding(t *testing.T) {
	data, err := json.Marshal(Response{})
	require.NoError(t, err)
	assert.NotContains(t, string(data), "request_input_usage")
	data, err = json.Marshal(ResponseEvidence{})
	require.NoError(t, err)
	assert.NotContains(t, string(data), "request_input_usage")
	response, err := CloneResponse(&Response{Usage: TokenUsage{InputTokens: 20, TotalTokens: 20}, RequestInputUsage: RequestInputUsage{Present: true}})
	require.NoError(t, err)
	assert.Equal(t, RequestInputUsage{Present: true}, response.RequestInputUsage)
	_, err = RestoreOutputValidationError(OutputValidationUsage, errors.New("cause"), ResponseEvidence{RequestInputUsage: RequestInputUsage{Present: true}}, nil)
	require.NoError(t, err)
}

func stringMustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}
