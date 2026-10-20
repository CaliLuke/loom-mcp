package mcp

import (
	"errors"
	"fmt"
	"testing"

	loom "github.com/CaliLuke/loom/pkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type responseStatusError struct{ status int }

func TestPublicErrorFormatting(t *testing.T) {
	inner := loom.WithErrorRemedy(loom.PermanentError("inner", "inner text"), &loom.ErrorRemedy{
		Code: "inner_code", SafeMessage: "Inner public.", RetryHint: "inner hint",
	})
	for _, tt := range []struct {
		name     string
		err      error
		code     string
		message  string
		safe     string
		recovery string
	}{
		{"nil", nil, "", "Tool execution failed.", "Fallback.", ""},
		{"unknown", errors.New("private"), "", "internal server error", "Fallback.", ""},
		{"named", loom.PermanentError("named", "Named public."), "named", "Named public.", "Fallback.", ""},
		{"wrapped remedy", fmt.Errorf("private: %w", inner), "inner_code", "Inner public.", "Inner public.", "inner hint"},
		{"single join", errors.Join(inner), "inner_code", "Inner public.", "Inner public.", "inner hint"},
		{"joined", errors.Join(inner, errors.New("cleanup")), "", "internal server error", "Fallback.", ""},
		{"reversed", errors.Join(errors.New("cleanup"), inner), "", "internal server error", "Fallback.", ""},
		{"outer owner", responseOwnerError{inner, "outer"}, "outer", "inner text", "Fallback.", ""},
		{"nil remedy", responseRemedyError{inner, nil}, "inner", "Inner public.", "Fallback.", ""},
		{"blank remedy", responseRemedyError{inner, &loom.ErrorRemedy{Code: " ", SafeMessage: " ", RetryHint: " "}}, "inner", "Tool execution failed.", "Fallback.", ""},
		{"explicit joined remedy", responseRemedyError{errors.Join(inner, errors.New("cleanup")), &loom.ErrorRemedy{Code: " whole ", SafeMessage: " Whole. ", RetryHint: " Retry. "}}, "whole", "Whole.", "Whole.", "Retry."},
		{"status bad request", responseStatusError{400}, "", "internal server error", "Fallback.", ""},
		{"status not found", fmt.Errorf("private: %w", responseStatusError{404}), "", "internal server error", "Fallback.", ""},
		{"impersonated", responseImpersonatorError{errors.New("private")}, "", "internal server error", "Fallback.", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code := tt.code
			if code == "" {
				code = "internal_error"
			}
			formatted := fmt.Sprintf("[%s] %s", code, tt.message)
			if tt.recovery != "" {
				formatted += "\nRecovery: " + tt.recovery
			}
			assert.Equal(t, formatted, FormatToolError(tt.err))

			code = tt.code
			if code == "" {
				code = "default_code"
			}
			safe := loom.ResolveErrorResponse(SafeError(tt.err, "default_code", "Fallback."))
			assert.Equal(t, code, safe.Name)
			require.NotNil(t, safe.Remedy)
			assert.Equal(t, &loom.ErrorRemedy{Code: code, SafeMessage: tt.safe}, safe.Remedy)
			input := loom.ResolveErrorResponse(NewToolInputError(tt.err, "default_code", "Default repair."))
			recovery := tt.recovery
			if recovery == "" {
				recovery = "Default repair."
			}
			assert.Equal(t, code, input.Name)
			assert.Equal(t, &loom.ErrorRemedy{Code: code, SafeMessage: tt.message, RetryHint: recovery}, input.Remedy)
		})
	}
	assert.Equal(t, &loom.ErrorRemedy{Code: "inner_code", SafeMessage: "Inner public.", RetryHint: "inner hint"}, inner.Remedy)
}

func TestSafeErrorPreservesProtocolControlFlow(t *testing.T) {
	err := invalidClientInputTestError{"Repair input."}
	assert.Equal(t, err, SafeError(err, "fallback", "Fallback."))
}

func (responseStatusError) Error() string {
	return "private status detail"
}

func (e responseStatusError) StatusCode() int {
	return e.status
}
