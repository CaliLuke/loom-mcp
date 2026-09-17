package openai

import (
	"github.com/CaliLuke/loom-mcp/v2/features/model/internal/openaitoolname"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
)

// openAIToolCodec carries OpenAI-specific strict schemas and the shared
// request-scoped mappings between canonical and provider-visible tool names.
type openAIToolCodec struct {
	schemas map[string]rawjson.Message
	names   *openaitoolname.Codec
}

// wireName returns the OpenAI-facing function name for a canonical tool
// identifier. Names registered by encodeTools use the recorded mapping; any
// other name is sanitized deterministically.
func (c *openAIToolCodec) wireName(canonical string) string {
	if c == nil {
		return openaitoolname.Sanitize(canonical)
	}
	return c.names.WireName(canonical)
}

// canonicalName translates an OpenAI-facing function name back to the
// canonical tool identifier. Names without a recorded mapping pass through.
func (c *openAIToolCodec) canonicalName(wire string) string {
	if c == nil {
		return wire
	}
	return c.names.CanonicalName(wire)
}
