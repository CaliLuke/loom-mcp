package hooks

import (
	"encoding/json/jsontext"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/require"
)

const (
	testRunID     = "run-1"
	testSessionID = "session-1"
)

func FuzzDecodeFromHookInput(f *testing.F) {
	f.Add(string(RunStarted), []byte(`{"RunContext":{"RunID":"run-1"},"Input":{"message":"hello"}}`))
	f.Add(string(PromptRendered), []byte(`{"PromptID":"example.agent.system","Version":"v1","Scope":{"SessionID":"session-1"}}`))
	f.Add(string(ToolCallScheduled), []byte(`{"ToolName":"search","ToolCallID":"call-1","Payload":{"q":"loom"}}`))
	f.Add("unknown", []byte(`{}`))
	f.Add(string(RunCompleted), []byte(`{`))

	f.Fuzz(func(t *testing.T, eventType string, payload []byte) {
		if len(eventType)+len(payload) > 1<<20 {
			return
		}
		input := &ActivityInput{
			Type:        EventType(eventType),
			EventKey:    "event-1",
			RunID:       testRunID,
			AgentID:     agent.Ident("agent-1"),
			SessionID:   testSessionID,
			TurnID:      "turn-1",
			TimestampMS: 1234,
			Payload:     rawjson.Message(payload),
		}
		decoded, err := DecodeFromHookInput(input)
		if !jsontext.Value(payload).IsValid() || !isSupportedHookEventType(input.Type) {
			require.Error(t, err)
			return
		}
		if err != nil {
			return
		}
		reencoded, err := EncodeToHookInput(decoded, decoded.TurnID())
		require.NoError(t, err)
		roundTrip, err := DecodeFromHookInput(reencoded)
		require.NoError(t, err)
		require.Equal(t, decoded.Type(), roundTrip.Type())
		require.Equal(t, decoded.EventKey(), roundTrip.EventKey())
	})
}

func isSupportedHookEventType(eventType EventType) bool {
	switch eventType {
	case RunStarted,
		RunCompleted,
		RunPaused,
		AwaitClarification,
		AwaitQuestions,
		AwaitTypedInput,
		AwaitConfirmation,
		AwaitExternalTools,
		ToolAuthorization,
		RunResumed,
		ToolCallScheduled,
		ToolCallArgsDelta,
		ToolResultReceived,
		ToolCallUpdated,
		PlannerNote,
		ThinkingBlock,
		AssistantMessage,
		AssistantTurnCommitted,
		RetryHintIssued,
		MemoryAppended,
		PolicyDecision,
		Usage,
		HardProtectionTriggered,
		RunPhaseChanged,
		ChildRunLinked,
		PromptRendered:
		return true
	default:
		return false
	}
}

func TestDecodeFromHookInput_ToolResultReceivedPreservesServerDataBytes(t *testing.T) {
	agentID := agent.Ident("agent-1")
	toolName := tools.Ident("svc.tools.lookup")
	toolCallID := "call-1"

	resultJSON := rawjson.Message([]byte(`{"summary":"ok"}`))
	serverData := rawjson.Message([]byte(`[{"kind":"example.topology","data":{"hello":"world","n":1}}]`))

	ev := NewToolResultReceivedEvent(
		testRunID,
		agentID,
		testSessionID,
		toolName,
		toolCallID,
		"",
		nil,
		resultJSON,
		serverData,
		"preview",
		nil,
		250*time.Millisecond,
		nil,
		nil,
		nil,
	)

	in, err := EncodeToHookInput(ev, "")
	require.NoError(t, err)

	decoded, err := DecodeFromHookInput(in)
	require.NoError(t, err)

	tr, ok := decoded.(*ToolResultReceivedEvent)
	require.True(t, ok)
	require.Equal(t, toolName, tr.ToolName)
	require.Equal(t, toolCallID, tr.ToolCallID)
	require.JSONEq(t, string(serverData), string(tr.ServerData))
}

func TestDecodeFromHookInput_PromptRenderedRoundTrip(t *testing.T) {
	agentID := agent.Ident("agent-1")

	ev := NewPromptRenderedEvent(
		testRunID,
		agentID,
		testSessionID,
		"example.agent.system",
		"v3",
		prompt.Scope{
			SessionID: testSessionID,
			Labels: map[string]string{
				"account": "acme",
				"region":  "west",
			},
		},
	)

	in, err := EncodeToHookInput(ev, "turn-1")
	require.NoError(t, err)

	decoded, err := DecodeFromHookInput(in)
	require.NoError(t, err)

	got, ok := decoded.(*PromptRenderedEvent)
	require.True(t, ok)
	require.Equal(t, testRunID, got.RunID())
	require.Equal(t, string(agentID), got.AgentID())
	require.Equal(t, testSessionID, got.SessionID())
	require.Equal(t, "turn-1", got.TurnID())
	require.Equal(t, prompt.Ident("example.agent.system"), got.PromptID)
	require.Equal(t, "v3", got.Version)
	require.Equal(t, testSessionID, got.Scope.SessionID)
	require.Equal(t, "acme", got.Scope.Labels["account"])
	require.Equal(t, "west", got.Scope.Labels["region"])
}

func TestEncodeToHookInputPreservesEventIdentity(t *testing.T) {
	agentID := agent.Ident("agent-1")

	ev := NewPromptRenderedEvent(
		testRunID,
		agentID,
		testSessionID,
		"example.agent.system",
		"v3",
		prompt.Scope{SessionID: testSessionID},
	)

	in, err := EncodeToHookInput(ev, "turn-1")
	require.NoError(t, err)
	require.Equal(t, ev.Timestamp(), in.TimestampMS)
	require.Equal(t, ev.EventKey(), in.EventKey)

	decoded, err := DecodeFromHookInput(in)
	require.NoError(t, err)
	require.Equal(t, ev.Timestamp(), decoded.Timestamp())
	require.Equal(t, ev.EventKey(), decoded.EventKey())
}
