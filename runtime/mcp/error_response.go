package mcp

import (
	"fmt"
	"strings"

	loom "github.com/CaliLuke/loom/pkg"
)

// FormatToolError returns client-safe text for a failed tool result. The public
// code and recovery hint belong to the complete error's resolved response;
// independent joined causes cannot supply either field.
func FormatToolError(err error) string {
	response := loom.ResolveErrorResponse(err)
	code := errorResponseCode(response, "internal_error")
	message := strings.TrimSpace(loom.ErrorSafeMessage(err))
	if message == "" {
		message = "Tool execution failed."
	}
	if response.Remedy != nil {
		if recovery := strings.TrimSpace(response.Remedy.RetryHint); recovery != "" {
			return fmt.Sprintf("[%s] %s\nRecovery: %s", code, message, recovery)
		}
	}
	return fmt.Sprintf("[%s] %s", code, message)
}

// SafeError converts a handler failure into a public named error. An explicit
// remedy may override the fallback message; diagnostic text remains private.
// Protocol control-flow errors pass through unchanged. An unnamed failure uses
// defaultCode; HTTP status interfaces do not declare MCP error names.
func SafeError(err error, defaultCode, fallbackMessage string) error {
	if IsInputRequired(err) || IsInvalidClientInput(err) {
		return err
	}
	response := loom.ResolveErrorResponse(err)
	code := errorResponseCode(response, defaultCode)
	message := strings.TrimSpace(fallbackMessage)
	if response.Remedy != nil {
		if safe := strings.TrimSpace(response.Remedy.SafeMessage); safe != "" {
			message = safe
		}
	}
	return loom.WithErrorRemedy(loom.PermanentError(code, "%s", message), &loom.ErrorRemedy{
		Code: code, SafeMessage: message,
	})
}

// NewToolInputError adds a tool's repair guidance to an input failure. Explicit
// response metadata takes precedence over defaults, and shared Loom message
// ownership determines which text is public. The input error is not modified.
func NewToolInputError(err error, defaultCode, defaultRecovery string) error {
	response := loom.ResolveErrorResponse(err)
	code := errorResponseCode(response, defaultCode)
	message := strings.TrimSpace(loom.ErrorSafeMessage(err))
	if message == "" {
		message = "Tool execution failed."
	}
	recovery := ""
	if response.Remedy != nil {
		recovery = strings.TrimSpace(response.Remedy.RetryHint)
	}
	if recovery == "" {
		recovery = defaultRecovery
	}
	return loom.WithErrorRemedy(loom.PermanentError(code, "%s", message), &loom.ErrorRemedy{
		Code: code, SafeMessage: message, RetryHint: recovery,
	})
}

func errorResponseCode(response loom.ErrorResponse, fallback string) string {
	if response.Remedy != nil {
		if code := strings.TrimSpace(response.Remedy.Code); code != "" {
			return code
		}
	}
	if name := strings.TrimSpace(response.Name); name != "" {
		return name
	}
	return fallback
}
