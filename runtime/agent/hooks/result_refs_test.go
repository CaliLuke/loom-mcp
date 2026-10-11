package hooks

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolResultReceivedRefsRoundTrip(t *testing.T) {
	for _, refs := range []tools.ResultRefs{{}, {Result: "result", ServerData: "server", Payload: "payload"}} {
		event := NewToolResultReceivedEvent(testRunID, "agent", testSessionID, "tool", "call", "", nil, nil, nil, "preview", nil, 0, nil, nil, nil)
		event.Refs = refs
		input, err := EncodeToHookInput(event, "turn")
		require.NoError(t, err)
		assert.Contains(t, string(input.Payload), `"refs":`)
		decoded, err := DecodeFromHookInput(input)
		require.NoError(t, err)
		assert.Equal(t, refs, decoded.(*ToolResultReceivedEvent).Refs)
	}
}
