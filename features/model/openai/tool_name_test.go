package openai

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/features/model/internal/openaitoolname"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIToolCodecNameRoundTrip(t *testing.T) {
	codec := &openAIToolCodec{names: openaitoolname.New(1)}
	_, err := codec.names.Register("toolset.tool")
	require.NoError(t, err)
	assert.Equal(t, "toolset_tool", codec.wireName("toolset.tool"))
	assert.Equal(t, "toolset.tool", codec.canonicalName("toolset_tool"))
	assert.Equal(t, "other_tool", codec.wireName("other.tool"))
	assert.Equal(t, "unknown", codec.canonicalName("unknown"))
}

func TestOpenAIToolCodecNilReceiver(t *testing.T) {
	var codec *openAIToolCodec
	assert.Equal(t, "runtime_tool_unavailable", codec.wireName("runtime.tool_unavailable"))
	assert.Equal(t, "toolset_tool", codec.canonicalName("toolset_tool"))
}
