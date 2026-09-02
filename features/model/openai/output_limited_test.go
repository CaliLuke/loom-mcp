package openai

import (
	"testing"

	"github.com/openai/openai-go/responses"
	"github.com/stretchr/testify/assert"
)

func TestOpenAIOutputLimited(t *testing.T) {
	tests := []struct {
		name   string
		status responses.ResponseStatus
		reason string
		want   bool
	}{
		{name: "maximum output tokens", status: responses.ResponseStatusIncomplete, reason: "max_output_tokens", want: true},
		{name: "content filter", status: responses.ResponseStatusIncomplete, reason: "content_filter"},
		{name: "completed", status: responses.ResponseStatusCompleted, reason: "max_output_tokens"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := &responses.Response{
				Status: test.status,
				IncompleteDetails: responses.ResponseIncompleteDetails{
					Reason: test.reason,
				},
			}
			assert.Equal(t, test.want, openAIOutputLimited(response))
		})
	}
	assert.False(t, openAIOutputLimited(nil))
}
