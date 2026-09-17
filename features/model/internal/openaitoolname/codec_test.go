package openaitoolname

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitize(t *testing.T) {
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
			assert.Equal(t, tt.want, Sanitize(tt.in))
		})
	}
}

func TestSanitizeTruncatesLongNamesDeterministically(t *testing.T) {
	long := "toolset." + strings.Repeat("a", 80)
	got := Sanitize(long)
	assert.Len(t, got, 64)
	assert.Equal(t, got, Sanitize(long))
	assert.NotEqual(t, got, Sanitize("toolset."+strings.Repeat("a", 79)+"b"))
}

func TestCodecRoundTripAndCollision(t *testing.T) {
	codec := New(2)
	wire, err := codec.Register("toolset.tool")
	require.NoError(t, err)
	assert.Equal(t, "toolset_tool", wire)
	assert.Equal(t, wire, codec.WireName("toolset.tool"))
	assert.Equal(t, "toolset.tool", codec.CanonicalName(wire))
	assert.Equal(t, "other_tool", codec.WireName("other.tool"))
	assert.Equal(t, "unknown", codec.CanonicalName("unknown"))

	_, err = codec.Register("toolset_tool")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "collides")
}

func TestCodecNilReceiver(t *testing.T) {
	var codec *Codec
	assert.Equal(t, "runtime_tool_unavailable", codec.WireName("runtime.tool_unavailable"))
	assert.Equal(t, "toolset_tool", codec.CanonicalName("toolset_tool"))
}
