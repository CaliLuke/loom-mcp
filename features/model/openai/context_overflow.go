package openai

import (
	"errors"

	openai "github.com/openai/openai-go/v3"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

const contextLengthExceededCode = "context_length_exceeded"

// openAIContextOverflow uses the provider code, never diagnostic message text.
func openAIContextOverflow(operation string, err error) *model.ProviderError {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) || apiErr.Code != contextLengthExceededCode {
		return nil
	}
	var requestID string
	if apiErr.Response != nil {
		requestID = apiErr.Response.Header.Get("x-request-id")
	}
	return openAIContextOverflowError(operation, apiErr.StatusCode, requestID, err)
}

func openAIContextOverflowError(operation string, status int, requestID string, cause error) *model.ProviderError {
	return model.NewProviderError("openai", operation, status, model.ProviderErrorKindContextOverflow,
		contextLengthExceededCode, "request exceeded the model context window", requestID, false, cause)
}
