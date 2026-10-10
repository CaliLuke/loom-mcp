// Package ollama wires local Ollama chat models into loom-mcp planners.
//
// Chat truncates input to num_ctx and has no documented chat overflow error to
// classify as model.ProviderErrorKindContextOverflow. This adapter deliberately
// has no context_overflow mapping. See:
// https://github.com/ollama/ollama/blob/main/server/prompt.go
package ollama
