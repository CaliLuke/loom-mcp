package hooks

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlannerStepPayloadRoundTrip(t *testing.T) {
	for _, event := range []Event{
		NewPlannerNoteEvent("run", "agent", "session", "note", nil),
		NewThinkingBlockEvent("run", "agent", "session", "thinking", "", nil, 0, true),
		NewPromptRenderedEvent("run", "agent", "session", "prompt", "v1", prompt.Scope{}),
		NewAssistantMessageEvent("run", "agent", "session", "text", nil),
		NewToolCallArgsDeltaEvent("run", "agent", "session", "call", "tool.read", "{}"),
		NewUsageEvent("run", "agent", "session", model.TokenUsage{TotalTokens: 1}),
		NewAssistantPresentationCommittedEvent("run", "agent", "session", []string{"presentation"}, nil),
	} {
		t.Run(string(event.Type()), func(t *testing.T) {
			before, err := EncodeToHookInput(event, "turn")
			require.NoError(t, err)
			var original map[string]jsontext.Value
			require.NoError(t, json.Unmarshal(before.Payload, &original))
			assert.NotContains(t, original, "planner_step")
			setter, ok := event.(interface{ SetPlannerStep(int) })
			require.True(t, ok, "built-in events must support planner activity stamps")
			setter.SetPlannerStep(2)
			input, err := EncodeToHookInput(event, "turn")
			require.NoError(t, err)
			var fields map[string]jsontext.Value
			require.NoError(t, json.Unmarshal(input.Payload, &fields))
			assert.Equal(t, jsontext.Value("2"), fields["planner_step"])
			delete(fields, "planner_step")
			assert.Equal(t, original, fields)
			decoded, err := DecodeFromHookInput(input)
			require.NoError(t, err)
			again, err := EncodeToHookInput(decoded, "turn")
			require.NoError(t, err)
			assert.JSONEq(t, string(input.Payload), string(again.Payload))
		})
	}
}
