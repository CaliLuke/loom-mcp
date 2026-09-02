package hooks

import (
	"errors"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHookCodecRoundTripsEveryEventType(t *testing.T) {
	t.Parallel()

	title := "Questions"
	total := 2
	scheduled := NewToolCallScheduledEvent(testRunID, "agent-1", testSessionID, "svc.tools.lookup", "call-1", rawjson.Message(`{"query":"loom"}`), "tools", "parent-1", 2)
	scheduled.DisplayHint = "Looking up loom"
	committed := NewAssistantPresentationCommittedEvent(testRunID, "agent-1", testSessionID, []string{"presentation-1"}, []*model.Message{{
		Role: model.ConversationRoleAssistant,
		Parts: []model.Part{model.CitationsPart{
			Text:      "supported",
			Citations: []model.Citation{{Title: "source"}},
		}},
		Meta: map[string]any{"provider": "test"},
	}})
	events := []Event{
		NewRunStartedEvent(testRunID, "agent-1", run.Context{SessionID: testSessionID}, map[string]any{"message": "hello"}),
		NewRunCompletedEvent(testRunID, "agent-1", testSessionID, "failed", run.PhaseFailed, errors.New("failed")),
		NewRunPhaseChangedEvent(testRunID, "agent-1", testSessionID, run.PhasePlanning),
		NewRunPausedEvent(testRunID, "agent-1", testSessionID, "approval", "operator", map[string]string{"team": "platform"}, map[string]any{"ticket": "T-1"}),
		NewRunResumedEvent(testRunID, "agent-1", testSessionID, "approved", "operator", map[string]string{"team": "platform"}, 2),
		NewPromptRenderedEvent(testRunID, "agent-1", testSessionID, "agent.system", "v2", prompt.Scope{SessionID: testSessionID}),
		NewAwaitClarificationEvent(testRunID, "agent-1", testSessionID, "await-1", "Which project?", []string{"project"}, "svc.tools.lookup", map[string]any{"project": "loom"}),
		NewAwaitQuestionsEvent(testRunID, "agent-1", testSessionID, "await-2", "svc.tools.lookup", "call-1", rawjson.Message(`{"query":"loom"}`), &title, []AwaitQuestion{{ID: "project", Prompt: "Project?"}}),
		NewAwaitTypedInputEvent(testRunID, "agent-1", testSessionID, "await-3", "Details", rawjson.Message(`{"type":"object"}`)),
		NewAwaitConfirmationEvent(testRunID, "agent-1", testSessionID, "await-4", "Confirm", "Continue?", "svc.tools.lookup", "call-1", rawjson.Message(`{"query":"loom"}`)),
		NewAwaitExternalToolsEvent(testRunID, "agent-1", testSessionID, "await-5", []AwaitToolItem{{ToolName: "svc.tools.lookup", ToolCallID: "call-1"}}),
		NewToolAuthorizationEvent(testRunID, "agent-1", testSessionID, "svc.tools.lookup", "call-1", true, "approved", "operator"),
		NewChildRunLinkedEvent(testRunID, "agent-1", testSessionID, "svc.tools.delegate", "call-1", "child-1", "agent-2"),
		NewToolCallArgsDeltaEvent(testRunID, "agent-1", testSessionID, "call-1", "svc.tools.lookup", `{"query":`),
		scheduled,
		NewToolCallUpdatedEvent(testRunID, "agent-1", testSessionID, "call-1", 3),
		NewToolResultReceivedEvent(testRunID, "agent-1", testSessionID, "svc.tools.lookup", "call-1", "parent-1", map[string]any{"answer": "loom"}, rawjson.Message(`{"answer":"loom"}`), rawjson.Message(`[{"kind":"private"}]`), "loom", &agent.Bounds{Total: &total}, 125*time.Millisecond, nil, nil, nil),
		NewRetryHintIssuedEvent(testRunID, "agent-1", testSessionID, "invalid_arguments", "svc.tools.lookup", "supply query"),
		NewAssistantMessageEvent(testRunID, "agent-1", testSessionID, "Done", map[string]any{"answer": "loom"}),
		committed,
		NewPlannerNoteEvent(testRunID, "agent-1", testSessionID, "checking", map[string]string{"stage": "lookup"}),
		NewThinkingBlockEvent(testRunID, "agent-1", testSessionID, "thought", "signature", []byte("redacted"), 1, true),
		NewPolicyDecisionEvent(testRunID, "agent-1", testSessionID, []tools.Ident{"svc.tools.lookup"}, policy.CapsState{}, map[string]string{"policy": "default"}, map[string]any{"rule": "allow"}),
		NewMemoryAppendedEvent(testRunID, "agent-1", testSessionID, 2),
		NewUsageEvent(testRunID, "agent-1", testSessionID, model.TokenUsage{InputTokens: 3, OutputTokens: 5}),
		NewHardProtectionEvent(testRunID, "agent-1", testSessionID, "no child calls", 1, 2, []tools.Ident{"svc.tools.delegate"}),
	}

	for _, event := range events {
		t.Run(string(event.Type()), func(t *testing.T) {
			t.Parallel()
			input, err := EncodeToHookInput(event, "turn-1")
			require.NoError(t, err)

			decoded, err := DecodeFromHookInput(input)
			require.NoError(t, err)
			assert.Equal(t, event.Type(), decoded.Type())
			assert.Equal(t, event.RunID(), decoded.RunID())
			assert.Equal(t, event.AgentID(), decoded.AgentID())
			assert.Equal(t, event.SessionID(), decoded.SessionID())
			assert.Equal(t, "turn-1", decoded.TurnID())
			assert.Equal(t, event.Timestamp(), decoded.Timestamp())
			assert.Equal(t, event.EventKey(), decoded.EventKey())

			reencoded, err := EncodeToHookInput(decoded, decoded.TurnID())
			require.NoError(t, err)
			assert.JSONEq(t, string(input.Payload), string(reencoded.Payload))
		})
	}
}

func TestAssistantTurnCodecPreservesLegacyMessageField(t *testing.T) {
	t.Parallel()

	message := &model.Message{
		Role:  model.ConversationRoleAssistant,
		Parts: []model.Part{model.TextPart{Text: "legacy"}},
	}
	input, err := EncodeToHookInput(NewAssistantTurnCommittedEvent(testRunID, "agent-1", testSessionID, message), "turn-1")
	require.NoError(t, err)
	require.Contains(t, string(input.Payload), `"Message"`)

	decoded, err := DecodeFromHookInput(input)
	require.NoError(t, err)
	committed := decoded.(*AssistantTurnCommittedEvent)
	require.Equal(t, message, committed.Message)
	require.Empty(t, committed.Messages)
	require.Empty(t, committed.PresentationIDs)
}

func TestHookCodecRejectsInvalidEnvelopes(t *testing.T) {
	t.Parallel()

	_, err := EncodeToHookInput(nil, "")
	require.ErrorContains(t, err, "event is required")
	var typedNil *RunStartedEvent
	_, err = EncodeToHookInput(typedNil, "")
	require.ErrorContains(t, err, "event is required")
	_, err = DecodeFromHookInput(nil)
	require.ErrorContains(t, err, "input is required")
	_, err = DecodeFromHookInput(&ActivityInput{Type: EventType("unknown"), Payload: rawjson.Message(`{}`)})
	require.ErrorContains(t, err, "unsupported hook event type")
	_, err = DecodeFromHookInput(&ActivityInput{Type: RunStarted, Payload: rawjson.Message(`{`)})
	require.ErrorContains(t, err, "decode run_started payload")
}
