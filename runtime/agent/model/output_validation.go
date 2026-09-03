package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
)

// OutputValidationKind identifies the request-contract rule that rejected
// provider output. Values are safe to persist and aggregate because they do not
// contain model text, tool names, arguments, identifiers, or schema paths.
type OutputValidationKind string

const (
	// OutputValidationResponseShape means the normalized response or chunk did
	// not have a valid canonical structure.
	OutputValidationResponseShape OutputValidationKind = "response_shape"

	// OutputValidationOutputBounds means provider output exceeded a byte,
	// nesting, collection, or generation limit.
	OutputValidationOutputBounds OutputValidationKind = "output_bounds"

	// OutputValidationToolIdentity means a tool call lacked a usable identity or
	// named a tool absent from the exact current request.
	OutputValidationToolIdentity OutputValidationKind = "tool_identity"

	// OutputValidationToolArguments means tool arguments were not canonical JSON
	// or did not satisfy the advertised input schema.
	OutputValidationToolArguments OutputValidationKind = "tool_arguments"

	// OutputValidationToolChoice means returned calls violated the request's
	// tool-choice rule.
	OutputValidationToolChoice OutputValidationKind = "tool_choice"

	// OutputValidationStructuredOutput means a requested structured completion
	// was missing, malformed, or failed its schema.
	OutputValidationStructuredOutput OutputValidationKind = "structured_output"

	// OutputValidationStreamProtocol means stream events were incomplete, out of
	// order, or otherwise violated the terminal protocol.
	OutputValidationStreamProtocol OutputValidationKind = "stream_protocol"

	// OutputValidationUsage means provider token accounting was malformed.
	OutputValidationUsage OutputValidationKind = "usage"
)

// ResponseEvidence is bounded identity for rejected output. It intentionally
// contains no response content.
type ResponseEvidence struct {
	Present     bool
	ByteCount   int
	Fingerprint [sha256.Size]byte
}

// OutputValidationError reports that provider output failed the immutable
// request contract. Error deliberately renders a constant public summary; use
// Kind, Evidence, Usage, and errors.Is/errors.As for structured diagnostics.
type OutputValidationError struct {
	kind     OutputValidationKind
	cause    error
	evidence ResponseEvidence
	usage    *TokenUsage
}

// Error returns a stable summary without rendering rejected model output or
// provider-authored identifiers.
func (e *OutputValidationError) Error() string {
	return "model output does not meet its request contract"
}

// Unwrap preserves the private validation cause for trusted in-process error
// inspection.
func (e *OutputValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Kind returns the closed category of the first rejecting check.
func (e *OutputValidationError) Kind() OutputValidationKind {
	if e == nil {
		return ""
	}
	return e.kind
}

// Evidence returns bounded identity for the rejected response.
func (e *OutputValidationError) Evidence() ResponseEvidence {
	if e == nil {
		return ResponseEvidence{}
	}
	return e.evidence
}

// Usage returns a detached copy of valid scalar usage retained from the
// rejected response.
func (e *OutputValidationError) Usage() *TokenUsage {
	if e == nil || e.usage == nil {
		return nil
	}
	usage := *e.usage
	return &usage
}

// RestoreOutputValidationError reconstructs a terminal output rejection after
// a trusted transport decodes its bounded fields.
func RestoreOutputValidationError(kind OutputValidationKind, cause error, evidence ResponseEvidence, usage *TokenUsage) (*OutputValidationError, error) {
	if !validOutputValidationKind(kind) {
		return nil, fmt.Errorf("model: invalid output validation kind %q", kind)
	}
	if cause == nil {
		return nil, errors.New("model: output validation cause is required")
	}
	var nested *OutputValidationError
	if errors.As(cause, &nested) {
		return nil, errors.New("model: output validation cause must not contain OutputValidationError")
	}
	if evidence.ByteCount < 0 {
		return nil, errors.New("model: output validation evidence byte count must be non-negative")
	}
	if usage != nil {
		if err := validateTokenUsage(*usage); err != nil {
			return nil, fmt.Errorf("model: output validation usage: %w", err)
		}
		copy := *usage
		usage = &copy
	}
	return &OutputValidationError{kind: kind, cause: cause, evidence: evidence, usage: usage}, nil
}

func newOutputValidationError(kind OutputValidationKind, cause error, evidence ResponseEvidence, usage *TokenUsage) *OutputValidationError {
	if cause == nil {
		panic("model: output validation error requires a cause")
	}
	var copiedUsage *TokenUsage
	if usage != nil && validateTokenUsage(*usage) == nil {
		copy := *usage
		copiedUsage = &copy
	}
	return &OutputValidationError{
		kind:     kind,
		cause:    cause,
		evidence: evidence,
		usage:    copiedUsage,
	}
}

func validOutputValidationKind(kind OutputValidationKind) bool {
	switch kind {
	case OutputValidationResponseShape,
		OutputValidationOutputBounds,
		OutputValidationToolIdentity,
		OutputValidationToolArguments,
		OutputValidationToolChoice,
		OutputValidationStructuredOutput,
		OutputValidationStreamProtocol,
		OutputValidationUsage:
		return true
	default:
		return false
	}
}

// ValidateTokenUsage checks that token counts are non-negative and that a
// non-zero total equals the sum of uncached input, output, and cache tokens.
func ValidateTokenUsage(usage TokenUsage) error {
	return validateTokenUsage(usage)
}

func validateTokenUsage(usage TokenUsage) error {
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.TotalTokens < 0 || usage.CacheReadTokens < 0 || usage.CacheWriteTokens < 0 {
		return errors.New("token usage contains a negative count")
	}
	total := usage.InputTokens
	if total > math.MaxInt-usage.OutputTokens {
		return errors.New("token usage component sum overflows")
	}
	total += usage.OutputTokens
	if total > math.MaxInt-usage.CacheReadTokens {
		return errors.New("token usage component sum overflows")
	}
	total += usage.CacheReadTokens
	if total > math.MaxInt-usage.CacheWriteTokens {
		return errors.New("token usage component sum overflows")
	}
	total += usage.CacheWriteTokens
	if usage.TotalTokens != 0 && usage.TotalTokens != total {
		return errors.New("token usage total does not equal input, output, and cache tokens")
	}
	return nil
}

func tokenUsageTotalKnown(usage TokenUsage) bool {
	if usage.TotalTokens != 0 {
		return true
	}
	return usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.CacheReadTokens == 0 && usage.CacheWriteTokens == 0
}
