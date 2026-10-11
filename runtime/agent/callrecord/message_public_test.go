package callrecord_test

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeMessageExactNumbers(t *testing.T) {
	data := []byte(`{"Meta":{"large":9007199254740993,"small":1e-30},"Parts":[{"Text":"done","type":"text"}],"Role":"assistant"}`)
	message, err := callrecord.DecodeMessage(data)
	require.NoError(t, err)
	encoded, err := callrecord.Encode(message)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `9007199254740993`)
	assert.Contains(t, string(encoded), `1e-30`)
}
