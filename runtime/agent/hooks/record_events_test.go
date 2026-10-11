package hooks

import (
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const recordEventProtocolVersion = 17

func TestRecordEvents(t *testing.T) {
	for _, kind := range []EventType{PlannerStep, RunSealed, ContextCompacted, ContextCompactionSkipped, ContextOverflow, ChildRunLinked} {
		assert.True(t, IsRecordEvent(kind), kind)
	}
	assert.False(t, IsRecordEvent(Usage))
	assert.False(t, IsRecordEvent(ContextRequestGrowth))
	key := runstore.CallKey{RunID: "run", Seq: 2, Attempt: 1}
	event := NewPlannerStepEvent("run", "agent", "session", 3, index.ViewRef{IndexHead: "head"}, []runstore.CallKey{key}, recordEventProtocolVersion, time.Unix(10, 0))
	input, err := EncodeToHookInput(event, "turn")
	require.NoError(t, err)
	assert.Equal(t, recordEventProtocolVersion, event.ProtocolVersion)
	assert.Contains(t, string(input.Payload), `"ProtocolVersion":`)
	assert.Equal(t, "planner_step:run:3", input.EventKey)
	assert.Equal(t, int64(10000), input.TimestampMS)
	decoded, err := DecodeFromHookInput(input)
	require.NoError(t, err)
	event.SetTurnID("turn")
	assert.Equal(t, event, decoded)
}

func TestPresentationCallKeysRoundTrip(t *testing.T) {
	key := runstore.CallKey{RunID: "run", Seq: 2, Attempt: 1}
	event := NewAssistantPresentationCommittedEvent("run", "agent", "session", []string{"recorded", "standalone"}, nil)
	event.CallKeys = []*runstore.CallKey{&key, nil}
	input, err := EncodeToHookInput(event, "turn")
	require.NoError(t, err)
	decoded, err := DecodeFromHookInput(input)
	require.NoError(t, err)
	committed, ok := decoded.(*AssistantTurnCommittedEvent)
	require.True(t, ok)
	assert.Equal(t, event.CallKeys, committed.CallKeys)
	assert.Equal(t, event.PresentationIDs, committed.PresentationIDs)
}

func TestRunSealedEventRoundTrip(t *testing.T) {
	timestamp := time.Unix(42, 123456789).UTC()
	event := NewRunSealedEvent("run", "head", recordEventProtocolVersion, timestamp)
	input, err := EncodeToHookInput(event, "")
	require.NoError(t, err)
	assert.Equal(t, recordEventProtocolVersion, event.ProtocolVersion)
	assert.Contains(t, string(input.Payload), `"ProtocolVersion":`)
	assert.Equal(t, "run_sealed:run", input.EventKey)
	assert.Equal(t, timestamp.UnixMilli(), input.TimestampMS)
	again, err := EncodeToHookInput(NewRunSealedEvent("run", "head", recordEventProtocolVersion, timestamp), "")
	require.NoError(t, err)
	assert.Equal(t, input, again)
	decoded, err := DecodeFromHookInput(input)
	require.NoError(t, err)
	assert.Equal(t, event, decoded)
}
