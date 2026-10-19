package model

import "errors"

// RequestInputUsage scopes input measurement to the original canonical request,
// separately from billed usage across provider continuation requests. Its zero
// value preserves legacy accounting. A present zero count is unmeasured or zero.
type RequestInputUsage struct {
	// Present distinguishes a scoped measurement from legacy billed accounting.
	Present bool `json:"present"`
	// InputTokens includes cached tokens in the original request, excluding
	// extra provider-generated tool-use prompts billed during generation.
	InputTokens int `json:"input_tokens"`
}

func validateRequestInputUsage(input RequestInputUsage, usage *TokenUsage) error {
	if input.InputTokens < 0 || (!input.Present && input.InputTokens != 0) {
		return errors.New("request input usage has an invalid count or presence")
	}
	if input.InputTokens == 0 {
		return nil
	}
	if usage == nil || validateTokenUsage(*usage) != nil {
		return errors.New("request input usage requires valid billed usage")
	}
	// Validated billable usage has a non-overflowing component sum.
	if input.InputTokens > usage.InputTokens+usage.CacheReadTokens+usage.CacheWriteTokens {
		return errors.New("request input usage exceeds billed input")
	}
	return nil
}

// retainedRequestInputUsage keeps valid facts, or an explicitly unmeasured
// initial request. Invalid facts must never fall back to aggregate billing.
func retainedRequestInputUsage(input RequestInputUsage, usage *TokenUsage) RequestInputUsage {
	if validateRequestInputUsage(input, usage) != nil {
		return RequestInputUsage{Present: true}
	}
	return input
}
