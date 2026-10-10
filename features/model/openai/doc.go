// Package openai wires OpenAI model clients into loom-mcp planners.
//
// The context_length_exceeded code in HTTP errors or streamed response.failed /
// error events returns a non-retryable model.ProviderError of kind
// context_overflow. Other provider error wrapping is unchanged.
package openai
