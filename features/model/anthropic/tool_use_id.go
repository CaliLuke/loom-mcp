package anthropic

import "fmt"

// toolUseIDCodec maps internal tool_use correlation IDs to provider-safe IDs
// for a single request and reverses the mapping when the provider echoes an
// ID back. Internal run-scoped IDs (for example,
// "runID/turnID/attempt-0/tool-name/0" or graph-loop "base#N" variants)
// violate Anthropic's tool_use id constraints (pattern ^[a-zA-Z0-9_-]+$) and
// must never be forwarded verbatim.
type toolUseIDCodec struct {
	canonToProv map[string]string
	provToCanon map[string]string
	next        int
}

func newToolUseIDCodec() *toolUseIDCodec {
	return &toolUseIDCodec{
		canonToProv: make(map[string]string),
		provToCanon: make(map[string]string),
	}
}

// isProviderSafeToolUseID reports whether id conforms to Anthropic's tool_use
// id constraints: pattern [a-zA-Z0-9_-]+ and length <= 64. The check is
// intentionally strict so internal correlation IDs (for example, run-scoped
// paths containing slashes) are never forwarded directly to the provider.
func isProviderSafeToolUseID(id string) bool {
	if id == "" {
		return false
	}
	if len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_':
		case r == '-':
		default:
			return false
		}
	}
	return true
}

// encode returns the provider-safe tool_use id for a canonical internal ID.
// Provider-safe IDs pass through unchanged; unsafe IDs are deterministically
// replaced ("t1", "t2", ...) so matching tool_use/tool_result pairs remain
// paired within the request.
func (c *toolUseIDCodec) encode(canonical string) string {
	if canonical == "" {
		return ""
	}
	if isProviderSafeToolUseID(canonical) {
		return canonical
	}
	if id, ok := c.canonToProv[canonical]; ok {
		return id
	}
	c.next++
	id := fmt.Sprintf("t%d", c.next)
	c.canonToProv[canonical] = id
	c.provToCanon[id] = canonical
	return id
}

// decode maps a provider-echoed tool_use id back to the canonical internal ID
// when it was substituted during encoding. IDs that were never substituted
// (including provider-minted IDs for new tool calls) pass through unchanged.
func (c *toolUseIDCodec) decode(provider string) string {
	if canonical, ok := c.provToCanon[provider]; ok {
		return canonical
	}
	return provider
}
