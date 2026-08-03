package anthropic

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	anthropicToolNameMaxBytes = 64
	anthropicToolNameHashLen  = 8
)

// sanitizeToolName maps a canonical tool identifier to an Anthropic-compatible
// provider name. It preserves the adapter's concise base-name policy, replaces
// unsupported runes with '_', and adds a stable hash suffix when the sanitized
// name would exceed Anthropic's 64-byte limit.
func sanitizeToolName(canonical string) string {
	if canonical == "" {
		return ""
	}
	base := anthropicToolNameBase(canonical)
	sanitized := sanitizeAnthropicToolName(base)
	if len(sanitized) <= anthropicToolNameMaxBytes {
		return sanitized
	}
	return truncateAnthropicToolName(canonical, sanitized)
}

func anthropicToolNameBase(canonical string) string {
	prefixPath, base, ok := strings.CutLast(canonical, ".")
	if !ok || base == "" {
		return canonical
	}
	if prefixPath == "" {
		return base
	}
	_, toolsetSuffix, ok := strings.CutLast(prefixPath, ".")
	if !ok || toolsetSuffix == "" {
		return base
	}
	prefix := toolsetSuffix + "_"
	if strings.HasPrefix(base, prefix) && len(base) > len(prefix) {
		return base[len(prefix):]
	}
	return base
}

func sanitizeAnthropicToolName(name string) string {
	if isProviderSafeToolName(name) {
		return name
	}
	out := make([]byte, 0, len(name))
	for _, r := range name {
		if isAllowedAnthropicToolNameRune(r) {
			out = append(out, string(r)...)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

func isProviderSafeToolName(name string) bool {
	if name == "" || len(name) > anthropicToolNameMaxBytes {
		return false
	}
	for _, r := range name {
		if !isAllowedAnthropicToolNameRune(r) {
			return false
		}
	}
	return true
}

func isAllowedAnthropicToolNameRune(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '_' || r == '-'
}

func truncateAnthropicToolName(canonical, sanitized string) string {
	sum := sha256.Sum256([]byte(canonical))
	suffix := hex.EncodeToString(sum[:])[:anthropicToolNameHashLen]
	prefixLen := anthropicToolNameMaxBytes - 1 - anthropicToolNameHashLen
	return sanitized[:prefixLen] + "_" + suffix
}
