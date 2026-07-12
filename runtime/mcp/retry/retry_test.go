package retry

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRetryableErrorFormatting(t *testing.T) {
	var nilError *RetryableError
	assert.Empty(t, nilError.Error())
	assert.Equal(t, "repair", (&RetryableError{Prompt: "repair"}).Error())
	assert.Equal(t, "repair: invalid params", (&RetryableError{Prompt: "repair", Cause: errors.New("invalid params")}).Error())
}

func TestBuildRepairPromptIsDeterministicWithOptionalSchema(t *testing.T) {
	withSchema := BuildRepairPrompt("tools/call", "query is required", `{"query":"example"}`, `{"required":["query"]}`)
	assert.Equal(t, `
Operation: tools/call
Schema: {"required":["query"]}
Error: query is required
Redo the operation now with valid parameters.
Use only valid schema fields and ensure required fields and types/enums are valid.
Example params: {"query":"example"}`, withSchema)

	withoutSchema := BuildRepairPrompt("tools/call", "invalid", `{}`, "")
	assert.NotContains(t, withoutSchema, "Schema:")
	assert.Contains(t, withoutSchema, "Error: invalid")
}
