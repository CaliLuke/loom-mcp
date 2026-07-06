package hooks

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/stretchr/testify/require"
)

func TestAwaitTypedInputCodecRoundTrip(t *testing.T) {
	evt := NewAwaitTypedInputEvent("run-1", "svc.agent", "sess-1", "approval", "Approval", rawjson.Message([]byte(`{"type":"object"}`)))

	input, err := EncodeToHookInput(evt, "turn-1")
	require.NoError(t, err)
	require.Equal(t, AwaitTypedInput, input.Type)

	decoded, err := DecodeFromHookInput(input)
	require.NoError(t, err)
	typed, ok := decoded.(*AwaitTypedInputEvent)
	require.True(t, ok)
	require.Equal(t, "approval", typed.ID)
	require.Equal(t, "Approval", typed.Title)
	require.JSONEq(t, `{"type":"object"}`, string(typed.Schema))
}
