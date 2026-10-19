package gemini

import (
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// ErrContinuationUnsupported means Gemini paused generation and the adapter
// cannot resume it. The incomplete response must not be used as an answer.
var ErrContinuationUnsupported = errors.New("gemini: CONTINUATION requires resuming an incomplete generation; automatic continuation is unsupported")

// continuationError rejects paused output before translating content or tool
// calls. Retain only valid scalar usage and the fact that a response arrived.
func continuationError(usage model.TokenUsage) error {
	retained := &usage
	if err := model.ValidateTokenUsage(usage); err != nil {
		retained = nil
	}
	rejection, err := model.RestoreOutputValidationError(
		model.OutputValidationResponseShape, ErrContinuationUnsupported,
		model.ResponseEvidence{Present: true}, retained,
	)
	if err != nil {
		return fmt.Errorf("gemini: construct continuation rejection: %w", err)
	}
	return rejection
}
