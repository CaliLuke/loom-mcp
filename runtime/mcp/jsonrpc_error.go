package mcp

import (
	"errors"
	"strings"

	loom "github.com/CaliLuke/loom/pkg"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

const (
	jsonRPCInitializeMethod     = "initialize"
	jsonRPCInternalErrorMessage = "Internal error."
)

// NormalizeJSONRPCError converts handler errors into typed JSON-RPC errors.
// Existing typed errors pass through unchanged. Untyped initialize errors are
// invalid parameters because the SDK uses them for duplicate initialization.
// All unknown errors fail closed as internal errors without exposing details.
func NormalizeJSONRPCError(method string, err error) error {
	if err == nil {
		return nil
	}

	var rpcErr *jsonrpc.Error
	if errors.As(err, &rpcErr) {
		return rpcErr
	}

	if method == jsonRPCInitializeMethod {
		return &jsonrpc.Error{
			Code:    jsonrpc.CodeInvalidParams,
			Message: err.Error(),
		}
	}
	if IsInvalidClientInput(err) {
		return &jsonrpc.Error{
			Code:    jsonrpc.CodeInvalidParams,
			Message: jsonRPCSafeMessage(err, "Invalid client input."),
		}
	}

	name := strings.TrimSpace(loom.ErrorRemedyCode(err))
	if name == "" {
		var namer loom.LoomErrorNamer
		if errors.As(err, &namer) {
			name = strings.TrimSpace(namer.LoomErrorName())
		}
	}

	switch name {
	case "invalid_params", "not_found", "prompt_not_found", "resource_not_found":
		return &jsonrpc.Error{
			Code:    jsonrpc.CodeInvalidParams,
			Message: jsonRPCSafeMessage(err, "Invalid parameters."),
		}
	case "internal_error":
		return &jsonrpc.Error{
			Code:    jsonrpc.CodeInternalError,
			Message: jsonRPCInternalMessage(err),
		}
	default:
		return &jsonrpc.Error{
			Code:    jsonrpc.CodeInternalError,
			Message: jsonRPCInternalErrorMessage,
		}
	}
}

func jsonRPCSafeMessage(err error, fallback string) string {
	message := strings.TrimSpace(loom.ErrorSafeMessage(err))
	if message == "" {
		return fallback
	}
	return message
}

func jsonRPCInternalMessage(err error) string {
	remedy := loom.ExtractErrorRemedy(err)
	if remedy == nil {
		return jsonRPCInternalErrorMessage
	}
	message := strings.TrimSpace(remedy.SafeMessage)
	if message == "" {
		return jsonRPCInternalErrorMessage
	}
	return message
}
