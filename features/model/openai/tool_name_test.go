package openai

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeOpenAIToolName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "already safe", in: "lookup", want: "lookup"},
		{name: "canonical dotted ident", in: "toolset.tool", want: "toolset_tool"},
		{name: "nested namespaces", in: "atlas.read.get_time_series", want: "atlas_read_get_time_series"},
		{name: "disallowed runes", in: "tool:name/with spaces", want: "tool_name_with_spaces"},
		{name: "hyphen and digits preserved", in: "toolset.v2-tool", want: "toolset_v2-tool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sanitizeOpenAIToolName(tt.in))
		})
	}
}

func TestSanitizeOpenAIToolNameTruncatesLongNames(t *testing.T) {
	long := "toolset." + strings.Repeat("a", 80)
	got := sanitizeOpenAIToolName(long)
	assert.Len(t, got, 64)
	assert.Equal(t, got, sanitizeOpenAIToolName(long), "mapping must be deterministic")

	other := "toolset." + strings.Repeat("a", 79) + "b"
	assert.NotEqual(t, got, sanitizeOpenAIToolName(other), "hash suffix must keep long names unique")
}

func TestOpenAIToolCodecNameRoundTrip(t *testing.T) {
	codec := &openAIToolCodec{
		canonToSan: map[string]string{"toolset.tool": "toolset_tool"},
		sanToCanon: map[string]string{"toolset_tool": "toolset.tool"},
	}
	assert.Equal(t, "toolset_tool", codec.wireName("toolset.tool"))
	assert.Equal(t, "toolset.tool", codec.canonicalName("toolset_tool"))

	assert.Equal(t, "other_tool", codec.wireName("other.tool"), "unmapped names fall back to deterministic sanitization")
	assert.Equal(t, "unknown", codec.canonicalName("unknown"), "unmapped wire names pass through")
}

func TestOpenAIToolCodecNilReceiver(t *testing.T) {
	var codec *openAIToolCodec
	assert.Equal(t, "runtime_tool_unavailable", codec.wireName("runtime.tool_unavailable"))
	assert.Equal(t, "toolset_tool", codec.canonicalName("toolset_tool"))
}
