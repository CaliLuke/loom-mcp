package model

import (
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRestoreOutputValidationErrorPreservesBoundedFields(t *testing.T) {
	cause := errors.New("trusted private cause")
	evidence := ResponseEvidence{Present: true, ByteCount: 42, Fingerprint: [32]byte{1, 2, 3}}
	usage := &TokenUsage{Model: "model", InputTokens: 2, OutputTokens: 3, TotalTokens: 5}

	restored, err := RestoreOutputValidationError(OutputValidationToolArguments, cause, evidence, usage)
	require.NoError(t, err)
	usage.InputTokens = 99

	assert.Equal(t, "model output does not meet its request contract", restored.Error())
	assert.Equal(t, OutputValidationToolArguments, restored.Kind())
	assert.Equal(t, evidence, restored.Evidence())
	assert.Equal(t, 2, restored.Usage().InputTokens)
	require.ErrorIs(t, restored, cause)

	copy := restored.Usage()
	copy.InputTokens = 100
	assert.Equal(t, 2, restored.Usage().InputTokens)
}

func TestRestoreOutputValidationErrorRejectsInvalidFields(t *testing.T) {
	validCause := errors.New("cause")
	tests := []struct {
		name     string
		kind     OutputValidationKind
		cause    error
		evidence ResponseEvidence
		usage    *TokenUsage
	}{
		{name: "unknown kind", kind: "unknown", cause: validCause},
		{name: "missing cause", kind: OutputValidationUsage},
		{name: "negative evidence", kind: OutputValidationUsage, cause: validCause, evidence: ResponseEvidence{ByteCount: -1}},
		{name: "invalid usage", kind: OutputValidationUsage, cause: validCause, usage: &TokenUsage{InputTokens: -1}},
		{name: "overflowing usage", kind: OutputValidationUsage, cause: validCause, usage: &TokenUsage{InputTokens: math.MaxInt, OutputTokens: 1}},
		{
			name:  "nested classification",
			kind:  OutputValidationUsage,
			cause: newOutputValidationError(OutputValidationUsage, validCause, ResponseEvidence{}, nil),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := RestoreOutputValidationError(test.kind, test.cause, test.evidence, test.usage)
			require.Error(t, err)
		})
	}
}
