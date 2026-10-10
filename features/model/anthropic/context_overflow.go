package anthropic

import (
	"encoding/json/v2"
	"errors"
	"net/http"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// anthropicContextOverflow recognizes only the documented invalid-request
// prefix: https://platform.claude.com/docs/en/build-with-claude/context-windows.
// Other Anthropic errors retain their existing wrapping contract.
func anthropicContextOverflow(operation string, err error) *model.ProviderError {
	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return nil
	}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decoded := json.Unmarshal([]byte(apiErr.RawJSON()), &body) == nil
	if !decoded || body.Error.Type != "invalid_request_error" || !strings.HasPrefix(body.Error.Message, "prompt is too long") {
		return nil
	}
	return model.NewProviderError("anthropic", operation, apiErr.StatusCode, model.ProviderErrorKindContextOverflow,
		"invalid_request_error", "request exceeded the model context window", apiErr.RequestID, false, err)
}

func anthropicContextWindowStop(operation string) *model.ProviderError {
	return model.NewProviderError("anthropic", operation, 0, model.ProviderErrorKindContextOverflow,
		string(sdk.StopReasonModelContextWindowExceeded), "request exceeded the model context window", "", false, nil)
}
