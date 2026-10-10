package bedrock

import (
	"strings"

	brtypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// bedrockContextOverflowMessage matches known model-specific validation text.
// AWS documents "Input is too long for requested model":
// https://repost.aws/knowledge-center/bedrock-validation-exception-errors
// Claude documents "prompt is too long":
// https://platform.claude.com/docs/en/build-with-claude/context-windows
// The Titan "Too many input tokens" body is captured at:
// https://www.philschmid.de/amazon-titan-embeddings
func bedrockContextOverflowMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "input is too long for requested model") ||
		strings.Contains(message, "prompt is too long") ||
		strings.Contains(message, "too many input tokens")
}

func bedrockContextWindowStop(operation string) *model.ProviderError {
	return model.NewProviderError(bedrockProviderName, operation, 0, model.ProviderErrorKindContextOverflow,
		string(brtypes.StopReasonModelContextWindowExceeded), "request exceeded the model context window", "", false, nil)
}
