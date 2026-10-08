package bedrock

import "github.com/CaliLuke/loom-mcp/v2/features/model/internal/toolname"

// SanitizeToolName maps a canonical tool identifier (for example, "atlas.read.get_time_series")
// to a Bedrock-compatible tool name.
//
// Bedrock imposes stricter tool name constraints than other providers. The tool name
// string surfaced to the model (and echoed back in tool_use blocks) must match the
// name registered in the tool configuration. This function implements the exact
// mapping used by the Bedrock adapter when constructing tool configurations.
//
// Contract:
//   - The mapping is deterministic.
//   - The mapping preserves canonical namespace information (".") by replacing dots
//     with underscores.
//   - The result contains only characters allowed by Bedrock: [a-zA-Z0-9_-]+.
//     Any other rune is replaced with '_'.
//   - The result is at most 64 bytes long. If the sanitized name exceeds the limit,
//     it is truncated and a stable hash suffix is appended to preserve uniqueness.
//
// Note: Callers should treat the output as provider-visible. Internally, loom-mcp
// continues to use canonical tool identifiers; the adapter translates tool_use names
// back to canonical IDs using the per-request reverse map.
func SanitizeToolName(in string) string {
	return toolname.Sanitize(in)
}
