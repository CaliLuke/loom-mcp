package gemini

import (
	"errors"
	"net/http"
	"strings"

	"google.golang.org/genai"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// geminiContextOverflow requires both the invalid-argument status and token
// message; other 400s retain their existing behavior. The phrase covers bodies
// with and without the input count:
// https://github.com/googleapis/python-genai/issues/2050
// https://github.com/google-gemini/gemini-cli/issues/22609
func geminiContextOverflow(err error) *model.ProviderError {
	var apiErr genai.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != http.StatusBadRequest || apiErr.Status != "INVALID_ARGUMENT" ||
		!strings.Contains(apiErr.Message, "exceeds the maximum number of tokens") {
		return nil
	}
	return model.NewProviderError("gemini", "generate_content", apiErr.Code, model.ProviderErrorKindContextOverflow,
		apiErr.Status, "request exceeded the model context window", "", false, err)
}
