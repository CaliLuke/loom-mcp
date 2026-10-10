// Package anthropic wires Anthropic Claude model clients into loom-mcp planners,
// including native prompt-cache breakpoints for request policies and explicit
// cache checkpoint message parts.
//
// Documented prompt-too-long HTTP 400 errors and model_context_window_exceeded
// stops return a non-retryable model.ProviderError with kind context_overflow.
// Other error wrapping is unchanged; max_tokens remains output-limited.
package anthropic
