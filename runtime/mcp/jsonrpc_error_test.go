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

type invalidClientInputTestError struct {
	message string
}

func TestNormalizeJSONRPCError(t *testing.T) {
	t.Parallel()

	typed := &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "missing method"}
	tests := []struct {
		name        string
		method      string
		err         error
		wantCode    int64
		wantMessage string
		wantSame    bool
	}{
		{
			name:     "typed error passes through",
			err:      typed,
			wantSame: true,
		},
		{
			name:     "wrapped typed error passes through",
			err:      fmt.Errorf("transport context: %w", typed),
			wantSame: true,
		},
		{
			name:        "duplicate initialize is invalid parameters",
			method:      "initialize",
			err:         errors.New(`duplicate "initialize" received`),
			wantCode:    jsonrpc.CodeInvalidParams,
			wantMessage: `duplicate "initialize" received`,
		},
		{
			name:        "invalid parameters preserve safe remedy",
			err:         remediedError("invalid_params", "private validation detail", "Invalid resource request."),
			wantCode:    jsonrpc.CodeInvalidParams,
			wantMessage: "Invalid resource request.",
		},
		{
			name:        "invalid client input preserves repair message",
			err:         invalidClientInputTestError{"response count does not match pending request count"},
			wantCode:    jsonrpc.CodeInvalidParams,
			wantMessage: "response count does not match pending request count",
		},
		{
			name:        "resource not found uses public service message",
			err:         loom.PermanentError("resource_not_found", "Unknown resource: secret://missing"),
			wantCode:    jsonrpc.CodeInvalidParams,
			wantMessage: "Unknown resource: secret://missing",
		},
		{
			name:        "internal error preserves declared safe remedy",
			err:         remediedError("internal_error", "database password leaked", "Resource read failed."),
			wantCode:    jsonrpc.CodeInternalError,
			wantMessage: "Resource read failed.",
		},
		{
			name:        "internal error without remedy hides detail",
			err:         loom.PermanentError("internal_error", "database password leaked"),
			wantCode:    jsonrpc.CodeInternalError,
			wantMessage: "Internal error.",
		},
		{
			name:        "unknown named error fails closed",
			err:         loom.PermanentError("unexpected_failure", "private detail"),
			wantCode:    jsonrpc.CodeInternalError,
			wantMessage: "Internal error.",
		},
		{
			name:        "plain error fails closed",
			err:         errors.New("private detail"),
			wantCode:    jsonrpc.CodeInternalError,
			wantMessage: "Internal error.",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := NormalizeJSONRPCError(test.method, test.err)
			if test.wantSame {
				assert.Same(t, typed, actual)
				return
			}

			var rpcErr *jsonrpc.Error
			require.ErrorAs(t, actual, &rpcErr)
			assert.Equal(t, test.wantCode, rpcErr.Code)
			assert.Equal(t, test.wantMessage, rpcErr.Message)
			assert.NotZero(t, rpcErr.Code)
		})
	}
}

func TestNormalizeJSONRPCErrorNil(t *testing.T) {
	t.Parallel()

	require.NoError(t, NormalizeJSONRPCError("resources/read", nil))
}

func remediedError(code, detail, safeMessage string) error {
	return loom.WithErrorRemedy(
		loom.PermanentError(code, "%s", detail),
		&loom.ErrorRemedy{Code: code, SafeMessage: safeMessage},
	)
}

func (e invalidClientInputTestError) Error() string {
	return e.message
}

func (invalidClientInputTestError) InvalidClientInput() {}
