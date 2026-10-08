// Package openaitoolname translates canonical Loom tool identifiers to the
// restricted function names accepted by OpenAI-compatible Responses APIs.
package openaitoolname

import (
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/features/model/internal/toolname"
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
	return toolname.Sanitize(input)
}
