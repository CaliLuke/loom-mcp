package api

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/stretchr/testify/require"
)

func TestTypedInputAnswerSignalContract(t *testing.T) {
	answer := &TypedInputAnswer{
		RunID:   "run-1",
		ID:      "approval",
		Payload: rawjson.Message([]byte(`{"approved":true}`)),
	}

	require.Equal(t, SignalProvideTypedInput, "loom_mcp.runtime.provide.typed_input")
	require.Equal(t, "approval", answer.ID)
	require.JSONEq(t, `{"approved":true}`, string(answer.Payload))
}
