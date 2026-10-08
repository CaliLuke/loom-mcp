// Package toolname normalizes tool identifiers for providers with ASCII name limits.
package toolname

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	maxNameLen = 64
	hashLen    = 8
)

// Sanitize maps a canonical tool identifier to a provider-compatible tool
// name containing only [a-zA-Z0-9_-] and at most 64 bytes.
func Sanitize(input string) string {
	if input == "" {
		return ""
	}
	sanitized := sanitize(input)
	if len(sanitized) <= maxNameLen {
		return sanitized
	}
	sum := sha256.Sum256([]byte(input))
	suffix := hex.EncodeToString(sum[:])[:hashLen]
	prefixLen := max(maxNameLen-(1+hashLen), 1)
	return sanitized[:prefixLen] + "_" + suffix
}

func sanitize(input string) string {
	if isFastPath(input) {
		return strings.ReplaceAll(input, ".", "_")
	}
	out := make([]rune, 0, len(input))
	for _, r := range input {
		if r == '.' {
			r = '_'
		}
		if !allowed(r) {
			r = '_'
		}
		out = append(out, r)
	}
	return string(out)
}

func isFastPath(input string) bool {
	for _, r := range input {
		if r == '.' {
			r = '_'
		}
		if !allowed(r) {
			return false
		}
	}
	return true
}

func allowed(r rune) bool {
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
