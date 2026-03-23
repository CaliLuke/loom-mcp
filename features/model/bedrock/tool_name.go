package bedrock

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

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
	if in == "" {
		return ""
	}
	const maxLen = 64
	const hashLen = 8
	sanitized := sanitizeBedrockName(in)
	if len(sanitized) <= maxLen {
		return sanitized
	}
	return truncateSanitizedBedrockName(in, sanitized, maxLen, hashLen)
}

func sanitizeBedrockName(in string) string {
	if isFastPathBedrockName(in) {
		return strings.ReplaceAll(in, ".", "_")
	}
	out := make([]rune, 0, len(in))
	for _, r := range in {
		out = append(out, sanitizeBedrockRune(r))
	}
	return string(out)
}

func isFastPathBedrockName(in string) bool {
	for _, r := range in {
		if !isAllowedBedrockRune(replaceDotRune(r)) {
			return false
		}
	}
	return true
}

func sanitizeBedrockRune(r rune) rune {
	r = replaceDotRune(r)
	if isAllowedBedrockRune(r) {
		return r
	}
	return '_'
}

func replaceDotRune(r rune) rune {
	if r == '.' {
		return '_'
	}
	return r
}

func isAllowedBedrockRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '_', r == '-':
		return true
	default:
		return false
	}
}

func truncateSanitizedBedrockName(input, sanitized string, maxLen, hashLen int) string {
	sum := sha256.Sum256([]byte(input))
	suffix := hex.EncodeToString(sum[:])[:hashLen]
	prefixLen := max(maxLen-(1+hashLen), 1)
	return sanitized[:prefixLen] + "_" + suffix
}
