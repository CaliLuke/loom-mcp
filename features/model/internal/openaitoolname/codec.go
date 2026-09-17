// Package openaitoolname translates canonical Loom tool identifiers to the
// restricted function names accepted by OpenAI-compatible Responses APIs.
package openaitoolname

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	maxNameLen = 64
	hashLen    = 8
)

// Codec stores request-scoped canonical-to-wire and wire-to-canonical names.
type Codec struct {
	canonicalToWire map[string]string
	wireToCanonical map[string]string
}

// New creates an empty request-scoped name codec sized for count tools.
func New(count int) *Codec {
	return &Codec{
		canonicalToWire: make(map[string]string, count),
		wireToCanonical: make(map[string]string, count),
	}
}

// Register records one canonical name and returns its provider-safe wire name.
// It rejects distinct canonical names that map to the same wire name.
func (c *Codec) Register(canonical string) (string, error) {
	wire := Sanitize(canonical)
	if previous, ok := c.wireToCanonical[wire]; ok && previous != canonical {
		return "", fmt.Errorf("tool name %q sanitizes to %q which collides with %q", canonical, wire, previous)
	}
	c.canonicalToWire[canonical] = wire
	c.wireToCanonical[wire] = canonical
	return wire, nil
}

// WireName returns the registered wire name or a deterministic sanitized name.
func (c *Codec) WireName(canonical string) string {
	if c != nil {
		if wire, ok := c.canonicalToWire[canonical]; ok && wire != "" {
			return wire
		}
	}
	return Sanitize(canonical)
}

// CanonicalName returns the canonical name registered for wire. Unknown wire
// names pass through unchanged.
func (c *Codec) CanonicalName(wire string) string {
	if c != nil {
		if canonical, ok := c.wireToCanonical[wire]; ok && canonical != "" {
			return canonical
		}
	}
	return wire
}

// Sanitize maps a canonical tool identifier to an OpenAI-compatible function
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
