package openai

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
)

// openAIToolCodec carries per-request tool translation state: strict schemas
// keyed by canonical tool name, plus the mappings between canonical tool
// identifiers ("toolset.tool") and the sanitized names surfaced to OpenAI.
// OpenAI function names must match ^[a-zA-Z0-9_-]+$ and be at most 64
// characters, so canonical dotted identifiers cannot be sent verbatim.
type openAIToolCodec struct {
	schemas    map[string]rawjson.Message
	canonToSan map[string]string
	sanToCanon map[string]string
}

// sanitizeOpenAIToolName maps a canonical tool identifier (for example,
// "toolset.tool") to an OpenAI-compatible function name.
//
// Contract:
//   - The mapping is deterministic.
//   - Namespace information (".") is preserved by replacing dots with
//     underscores.
//   - The result contains only characters allowed by OpenAI: [a-zA-Z0-9_-]+.
//     Any other rune is replaced with '_'.
//   - The result is at most 64 bytes long. If the sanitized name exceeds the
//     limit, it is truncated and a stable hash suffix is appended to preserve
//     uniqueness.
//
// Callers should treat the output as provider-visible. Internally, loom-mcp
// continues to use canonical tool identifiers; the adapter translates
// function-call names back to canonical IDs using the per-request reverse map.
func sanitizeOpenAIToolName(in string) string {
	if in == "" {
		return ""
	}
	const maxLen = 64
	const hashLen = 8
	sanitized := sanitizeOpenAIName(in)
	if len(sanitized) <= maxLen {
		return sanitized
	}
	return truncateSanitizedOpenAIName(in, sanitized, maxLen, hashLen)
}

func sanitizeOpenAIName(in string) string {
	if isFastPathOpenAIName(in) {
		return strings.ReplaceAll(in, ".", "_")
	}
	out := make([]rune, 0, len(in))
	for _, r := range in {
		out = append(out, sanitizeOpenAIRune(r))
	}
	return string(out)
}

func isFastPathOpenAIName(in string) bool {
	for _, r := range in {
		if !isAllowedOpenAIRune(replaceOpenAIDotRune(r)) {
			return false
		}
	}
	return true
}

func sanitizeOpenAIRune(r rune) rune {
	r = replaceOpenAIDotRune(r)
	if isAllowedOpenAIRune(r) {
		return r
	}
	return '_'
}

func replaceOpenAIDotRune(r rune) rune {
	if r == '.' {
		return '_'
	}
	return r
}

func isAllowedOpenAIRune(r rune) bool {
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

func truncateSanitizedOpenAIName(input, sanitized string, maxLen, hashLen int) string {
	sum := sha256.Sum256([]byte(input))
	suffix := hex.EncodeToString(sum[:])[:hashLen]
	prefixLen := max(maxLen-(1+hashLen), 1)
	return sanitized[:prefixLen] + "_" + suffix
}

// wireName returns the OpenAI-facing function name for a canonical tool
// identifier. Names registered by encodeTools use the recorded mapping; any
// other name (for example, a transcript replay of a tool that is no longer
// configured) is sanitized deterministically so requests never carry names
// OpenAI rejects.
func (c *openAIToolCodec) wireName(canonical string) string {
	if c != nil {
		if sanitized, ok := c.canonToSan[canonical]; ok && sanitized != "" {
			return sanitized
		}
	}
	return sanitizeOpenAIToolName(canonical)
}

// canonicalName translates an OpenAI-facing function name back to the
// canonical tool identifier. Names without a recorded mapping are returned
// unchanged.
func (c *openAIToolCodec) canonicalName(wire string) string {
	if c != nil {
		if canonical, ok := c.sanToCanon[wire]; ok && canonical != "" {
			return canonical
		}
	}
	return wire
}
