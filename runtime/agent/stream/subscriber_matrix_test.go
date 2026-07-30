package stream

import (
	"context"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamSubscriberAwaitAuthorizationAndUsageMatrix(t *testing.T) {
	t.Parallel()

	title := "Choose"
	tests := []struct {
		name   string
		event  hooks.Event
		assert func(*testing.T, Event)
	}{
		{
			name:  "clarification",
			event: hooks.NewAwaitClarificationEvent("run-1", "agent-1", "sess-1", "await-1", "Which project?", []string{"project"}, "svc.tools.lookup", map[string]any{"project": "loom"}),
			assert: func(t *testing.T, event Event) {
				got := event.(AwaitClarification)
				assert.Equal(t, EventAwaitClarification, got.Type())
				assert.Equal(t, []string{"project"}, got.Data.MissingFields)
				assert.Equal(t, "svc.tools.lookup", got.Data.RestrictToTool)
				assert.Equal(t, "loom", got.Data.ExampleInput["project"])
			},
		},
		{
			name:  "confirmation",
			event: hooks.NewAwaitConfirmationEvent("run-1", "agent-1", "sess-1", "await-2", "Confirm", "Continue?", "svc.tools.lookup", "call-1", rawjson.Message(`{"project":"loom"}`)),
			assert: func(t *testing.T, event Event) {
				got := event.(AwaitConfirmation)
				assert.Equal(t, EventAwaitConfirmation, got.Type())
				assert.Equal(t, "Continue?", got.Data.Prompt)
				require.JSONEq(t, `{"project":"loom"}`, string(got.Data.Payload))
			},
		},
		{
			name: "questions",
			event: hooks.NewAwaitQuestionsEvent("run-1", "agent-1", "sess-1", "await-3", "svc.tools.lookup", "call-1", rawjson.Message(`{"project":"loom"}`), &title, []hooks.AwaitQuestion{{
				ID: "project", Prompt: "Project?", AllowMultiple: true, Options: []hooks.AwaitQuestionOption{{ID: "loom", Label: "Loom"}},
			}}),
			assert: func(t *testing.T, event Event) {
				got := event.(AwaitQuestions)
				assert.Equal(t, EventAwaitQuestions, got.Type())
				require.JSONEq(t, `{"project":"loom"}`, string(got.Data.Payload))
				require.Len(t, got.Data.Questions, 1)
				assert.True(t, got.Data.Questions[0].AllowMultiple)
				assert.Equal(t, "Loom", got.Data.Questions[0].Options[0].Label)
			},
		},
		{
			name: "external_tools",
			event: hooks.NewAwaitExternalToolsEvent("run-1", "agent-1", "sess-1", "await-4", []hooks.AwaitToolItem{{
				ToolName: "svc.tools.lookup", ToolCallID: "call-1", Payload: rawjson.Message(`{"project":"loom"}`),
			}}),
			assert: func(t *testing.T, event Event) {
				got := event.(AwaitExternalTools)
				assert.Equal(t, EventAwaitExternalTools, got.Type())
				require.Len(t, got.Data.Items, 1)
				require.JSONEq(t, `{"project":"loom"}`, string(got.Data.Items[0].Payload))
			},
		},
		{
			name:  "authorization",
			event: hooks.NewToolAuthorizationEvent("run-1", "agent-1", "sess-1", "svc.tools.lookup", "call-1", true, "approved", "user:1"),
			assert: func(t *testing.T, event Event) {
				got := event.(ToolAuthorization)
				assert.Equal(t, EventToolAuthorization, got.Type())
				assert.True(t, got.Data.Approved)
				assert.Equal(t, "user:1", got.Data.ApprovedBy)
			},
		},
		{
			name:  "usage",
			event: hooks.NewUsageEvent("run-1", "agent-1", "sess-1", model.TokenUsage{InputTokens: 3, OutputTokens: 5, Model: "test-model"}),
			assert: func(t *testing.T, event Event) {
				got := event.(Usage)
				assert.Equal(t, EventUsage, got.Type())
				assert.Equal(t, 3, got.Data.InputTokens)
				assert.Equal(t, 5, got.Data.OutputTokens)
				assert.Equal(t, "test-model", got.Data.Model)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sink := &mockSink{}
			subscriber, err := NewSubscriber(sink)
			require.NoError(t, err)
			require.NoError(t, subscriber.HandleEvent(context.Background(), test.event))
			require.Len(t, sink.events, 1)
			assert.Equal(t, "run-1", sink.events[0].RunID())
			assert.Equal(t, "sess-1", sink.events[0].SessionID())
			test.assert(t, sink.events[0])
		})
	}
}

func TestStreamSubscriberDetachesMutablePayloadBuffers(t *testing.T) {
	t.Parallel()

	sink := &mockSink{}
	subscriber, err := NewSubscriber(sink)
	require.NoError(t, err)

	confirmationPayload := rawjson.Message(`{"value":"original"}`)
	require.NoError(t, subscriber.HandleEvent(context.Background(), hooks.NewAwaitConfirmationEvent(
		"run-1", "agent-1", "sess-1", "await-1", "Confirm", "Continue?", "svc.tool", "call-1", confirmationPayload,
	)))
	confirmationPayload[10] = 'X'
	confirmation := sink.events[0].(AwaitConfirmation)
	require.JSONEq(t, `{"value":"original"}`, string(confirmation.Data.Payload))

	toolPayload := rawjson.Message(`{"value":"original"}`)
	require.NoError(t, subscriber.HandleEvent(context.Background(), hooks.NewToolCallScheduledEvent(
		"run-1", "agent-1", "sess-1", "svc.tool", "call-2", toolPayload, "", "", 0,
	)))
	toolPayload[10] = 'X'
	toolStart := sink.events[1].(ToolStart)
	require.JSONEq(t, `{"value":"original"}`, string(toolStart.Data.Payload))

	resultPayload := rawjson.Message(`{"value":"original"}`)
	require.NoError(t, subscriber.HandleEvent(context.Background(), hooks.NewToolResultReceivedEvent(
		"run-1", "agent-1", "sess-1", "svc.tool", "call-2", "", nil, resultPayload, nil, "", nil, 0, nil, nil, nil,
	)))
	resultPayload[10] = 'X'
	toolEnd := sink.events[2].(ToolEnd)
	require.JSONEq(t, `{"value":"original"}`, string(toolEnd.Data.Result))

	redacted := []byte("original")
	require.NoError(t, subscriber.HandleEvent(context.Background(), hooks.NewThinkingBlockEvent(
		"run-1", "agent-1", "sess-1", "", "", redacted, 0, true,
	)))
	redacted[0] = 'X'
	thought := sink.events[3].(PlannerThought)
	assert.Equal(t, []byte("original"), thought.Data.Redacted)
}

func TestStreamSubscriberProfilesAndErrors(t *testing.T) {
	t.Parallel()

	_, err := NewSubscriber(nil)
	require.ErrorContains(t, err, "stream sink is required")
	assert.Equal(t, DefaultProfile(), UserChatProfile())
	assert.Equal(t, DefaultProfile(), AgentDebugProfile())
	assert.Equal(t, StreamProfile{Usage: true, Workflow: true}, MetricsProfile())

	sinkErr := errors.New("sink failed")
	subscriber, err := NewSubscriber(&mockSink{err: sinkErr})
	require.NoError(t, err)
	err = subscriber.HandleEvent(context.Background(), hooks.NewUsageEvent("run-1", "agent-1", "sess-1", model.TokenUsage{}))
	require.ErrorIs(t, err, sinkErr)

	sink := &mockSink{}
	subscriber, err = NewSubscriber(sink)
	require.NoError(t, err)
	err = subscriber.HandleEvent(context.Background(), hooks.NewToolCallArgsDeltaEvent("run-1", "agent-1", "sess-1", "call-1", "", "{"))
	require.ErrorContains(t, err, "missing tool name")
	err = subscriber.HandleEvent(context.Background(), hooks.NewToolResultReceivedEvent("run-1", "agent-1", "sess-1", "", "call-1", "", nil, nil, nil, "", nil, 0, nil, nil, nil))
	require.ErrorContains(t, err, "missing tool_name")
	err = subscriber.HandleEvent(context.Background(), hooks.NewRunCompletedEvent("run-1", "agent-1", "sess-1", "success", run.Phase(""), nil))
	require.ErrorContains(t, err, "missing phase")

	disabled, err := NewSubscriberWithProfile(sink, StreamProfile{})
	require.NoError(t, err)
	require.NoError(t, disabled.HandleEvent(context.Background(), hooks.NewToolCallScheduledEvent("run-1", "agent-1", "sess-1", tools.Ident("svc.tool"), "call-1", nil, "", "", 0)))
}
