package callrecord

import (
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBodyRoundTrip(t *testing.T) {
	started := Started{Key: runstore.CallKey{RunID: "run", Seq: 12, Attempt: 2}, Activity: ActivityPlanner, TurnID: "turn", Step: 9, Started: time.Date(2026, 10, 10, 12, 13, 14, 123456789, time.UTC), Model: "provider", ModelID: "registered", Class: "class"}
	data, err := Encode(started)
	require.NoError(t, err)
	decoded, err := DecodeStarted(data)
	require.NoError(t, err)
	again, err := Encode(decoded)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(again))
	assert.Equal(t, started.Started, decoded.Started)
	assert.Empty(t, decoded.Context)
	response := runstore.Hash("response")
	for _, failure := range []*CallError{nil, {Kind: "stream_abandoned", Message: "not finalized"}} {
		completion := Completion{Response: &response, Error: failure, Usage: model.TokenUsage{InputTokens: 3, TotalTokens: 3}, Latency: 123456789 * time.Nanosecond}
		encoded, err := Encode(completion)
		require.NoError(t, err)
		assert.Contains(t, string(encoded), `"latency":123456789`)
		got, err := DecodeCompletion(encoded)
		require.NoError(t, err)
		assert.Equal(t, completion, got)
		assert.Nil(t, got.Facts)
		roundTrip, err := Encode(got)
		require.NoError(t, err)
		assert.Equal(t, string(encoded), string(roundTrip))
	}
}

func TestRequestPartsRejectUncommittedView(t *testing.T) {
	// Pending content must have been committed by the activity view loader first.
	_, _, err := BuildRequest(t.Context(), nil, "root", &model.Request{}, index.ViewRef{Pending: []index.MessageEntry{{ID: 1}}}, nil)
	require.ErrorContains(t, err, "committed")
}
