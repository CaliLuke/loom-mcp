package mcp

import (
	"errors"
	"fmt"
	"testing"

	loom "github.com/CaliLuke/loom/pkg"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type (
	responseOwnerError struct {
		error
		name string
	}
	responseRemedyError struct {
		error
		remedy *loom.ErrorRemedy
	}
	responseImpersonatorError struct{ error }
)

func TestJSONRPCResponseOwnership(t *testing.T) {
	missing := remediedError("resource_not_found", "private missing", "Public missing.")
	internal := remediedError("internal_error", "private internal", "Public failure.")
	for _, tt := range []struct {
		name    string
		err     error
		code    int64
		message string
	}{
		{"ordinary wrapper", fmt.Errorf("private wrapper: %w", missing), jsonrpc.CodeInvalidParams, "Public missing."},
		{"single cause join", errors.Join(missing), jsonrpc.CodeInvalidParams, "Public missing."},
		{"independent join", errors.Join(missing, internal), jsonrpc.CodeInternalError, "Internal error."},
		{"reversed join", errors.Join(internal, missing), jsonrpc.CodeInternalError, "Internal error."},
		{"wrapped join", fmt.Errorf("private wrapper: %w", errors.Join(missing, internal)), jsonrpc.CodeInternalError, "Internal error."},
		{"outer owner shadows child remedy", responseOwnerError{missing, "internal_error"}, jsonrpc.CodeInternalError, "Internal error."},
		{"custom As cannot supply owner", responseImpersonatorError{errors.New("private detail")}, jsonrpc.CodeInternalError, "Internal error."},
		{"explicit remedy owns joined failure", responseRemedyError{errors.Join(missing, internal), &loom.ErrorRemedy{Code: "invalid_params", SafeMessage: "Whole failure."}}, jsonrpc.CodeInvalidParams, "Whole failure."},
		{"nil remedy shadows inner metadata", responseRemedyError{internal, nil}, jsonrpc.CodeInternalError, "Internal error."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var actual *jsonrpc.Error
			require.ErrorAs(t, NormalizeJSONRPCError("resources/read", tt.err), &actual)
			assert.Equal(t, tt.code, actual.Code)
			assert.Equal(t, tt.message, actual.Message)
		})
	}
}

func TestErrorDataResponseOwnership(t *testing.T) {
	inner := loom.NewServiceError(errors.New("private detail"), "resource_not_found", true, true, true)
	inner.Remedy = &loom.ErrorRemedy{Code: "inner", RetryHint: "private branch hint"}
	for _, tt := range []struct {
		name string
		err  error
		want any
	}{
		{"single cause", fmt.Errorf("wrapper: %w", inner), &ErrorData{Name: inner.Name, Temporary: true, Timeout: true, Fault: true, Remedy: inner.Remedy}},
		{"joined causes", errors.Join(inner, errors.New("cleanup")), nil},
		{"reversed causes", errors.Join(errors.New("cleanup"), inner), nil},
		{"outer named owner", responseOwnerError{inner, "outer"}, &ErrorData{Name: "outer"}},
		{"nil remedy shadows inner", responseRemedyError{inner, nil}, &ErrorData{Name: inner.Name, Temporary: true, Timeout: true, Fault: true}},
		{"custom As cannot supply owner", responseImpersonatorError{errors.New("private detail")}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NewErrorData(tt.err))
		})
	}
}

func (e responseOwnerError) Unwrap() error {
	return e.error
}

func (e responseOwnerError) LoomErrorName() string {
	return e.name
}

func (e responseRemedyError) Unwrap() error {
	return e.error
}

func (e responseRemedyError) LoomErrorRemedy() *loom.ErrorRemedy {
	return e.remedy
}

func (responseImpersonatorError) As(target any) bool {
	if namer, ok := target.(*loom.LoomErrorNamer); ok {
		*namer = loom.PermanentError("resource_not_found", "fabricated owner")
		return true
	}
	return false
}
