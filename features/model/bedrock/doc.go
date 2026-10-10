// Package bedrock wires AWS Bedrock model clients into loom-mcp planners.
//
// Context-window ValidationException messages and model_context_window_exceeded
// stops return a non-retryable model.ProviderError with kind context_overflow,
// including stream receive errors. Only max_tokens is output-limited. A context
// overflow fails the planner activity until overflow recovery is implemented.
package bedrock
