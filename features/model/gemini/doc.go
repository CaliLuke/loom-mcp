// Package gemini wires Google Gemini model clients into loom-mcp planners.
//
// HTTP 400 INVALID_ARGUMENT token-overflow messages return a non-retryable
// model.ProviderError with kind context_overflow. Other error wrapping is
// unchanged. The Vertex backend uses the same classification; streaming remains
// unsupported.
package gemini
