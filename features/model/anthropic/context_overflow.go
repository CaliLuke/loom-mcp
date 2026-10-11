package anthropic

import (
	"encoding/json/v2"
	"errors"
	"net/http"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// anthropicContextOverflow recognizes Claude's context-limit diagnostics.
// https://platform.claude.com/docs/en/api/errors documents that SSE errors can
// arrive after HTTP 200; only stream operations accept that successful status.
// The older diagnostic is recorded at https://github.com/anthropics/claude-code/issues/52.
// Other Anthropic errors retain their existing wrapping contract.
func anthropicContextOverflow(operation string, err error) *model.ProviderError {
	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) {
		return nil
	}
	if apiErr.StatusCode != http.StatusBadRequest && !(operation == "messages.new stream" && apiErr.StatusCode == http.StatusOK) {
		return nil
	}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decoded := json.Unmarshal([]byte(apiErr.RawJSON()), &body) == nil
	if !decoded || body.Error.Type != "invalid_request_error" || !anthropicContextOverflowMessage(body.Error.Message) {
		return nil
	}
	return model.NewProviderError("anthropic", operation, apiErr.StatusCode, model.ProviderErrorKindContextOverflow,
		"invalid_request_error", "request exceeded the model context window", apiErr.RequestID, false, err)
}

func anthropicContextOverflowMessage(message string) bool {
	return strings.HasPrefix(message, "prompt is too long") ||
		strings.HasPrefix(message, "input length and `max_tokens` exceed context limit") ||
		strings.HasPrefix(message, "input length and max_tokens exceed context limit")
}

func anthropicContextWindowStop(operation string) *model.ProviderError {
	return model.NewProviderError("anthropic", operation, 0, model.ProviderErrorKindContextOverflow,
		string(sdk.StopReasonModelContextWindowExceeded), "request exceeded the model context window", "", false, nil)
}
