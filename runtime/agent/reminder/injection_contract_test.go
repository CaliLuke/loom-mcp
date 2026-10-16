package reminder_test

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/reminder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReminderInjectionPreservesToolHandshakeAndSourceMessages(t *testing.T) {
	input := []*model.Message{
		{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: "original"}}, Meta: map[string]any{"name": "source"}},
		nil,
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read"}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: "result"}}},
	}
	out := reminder.InjectMessages(input, []reminder.Reminder{
		{Text: " start ", Attachment: reminder.Attachment{Kind: reminder.AttachmentRunStart}},
		{Text: " "}, {Text: "<system-reminder>first</system-reminder>"}, {Text: "second"},
	})
	require.Len(t, out, 5)
	require.Len(t, out[0].Parts, 2)
	assert.Equal(t, "<system-reminder>start</system-reminder>", out[0].Parts[0].(model.TextPart).Text)
	assert.Nil(t, out[1])
	assert.Equal(t, input[2].Role, out[2].Role)
	assert.Equal(t, input[2].Parts, out[2].Parts)
	assert.Equal(t, input[3].Role, out[3].Role)
	assert.Equal(t, input[3].Parts, out[3].Parts)
	assert.Equal(t, "<system-reminder>first</system-reminder>\n\n<system-reminder>second</system-reminder>", out[4].Parts[0].(model.TextPart).Text)
	out[0].Parts[1] = model.TextPart{Text: "changed"}
	out[0].Meta["name"] = "changed"
	assert.Equal(t, "original", input[0].Parts[0].(model.TextPart).Text)
	assert.Equal(t, "source", input[0].Meta["name"])
}

func TestReminderInjectionWithoutUserAndWithEmptyText(t *testing.T) {
	input := []*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "answer"}}}}
	out := reminder.InjectMessages(input, []reminder.Reminder{{Text: "next"}})
	require.Len(t, out, 2)
	assert.Equal(t, model.ConversationRoleSystem, out[1].Role)
	assert.Equal(t, "<system-reminder>next</system-reminder>", out[1].Parts[0].(model.TextPart).Text)
	out = reminder.InjectMessages(input, []reminder.Reminder{{Text: " "}, {Text: " ", Attachment: reminder.Attachment{Kind: reminder.AttachmentRunStart}}})
	require.Len(t, out, 1)
	assert.Equal(t, input[0].Role, out[0].Role)
	assert.Equal(t, input[0].Parts, out[0].Parts)
	assert.Empty(t, reminder.InjectMessages(nil, []reminder.Reminder{{Text: "next"}}))
}

func TestReminderReplacementRetainsEmissionCapUntilRunCleared(t *testing.T) {
	engine := reminder.NewEngine()
	engine.AddReminder("run", reminder.Reminder{ID: "one", Text: "original", Priority: reminder.TierGuidance, MaxPerRun: 1})
	require.Len(t, engine.Snapshot("run"), 1)
	engine.AddReminder("run", reminder.Reminder{ID: "one", Text: "replacement", Priority: reminder.TierGuidance, MaxPerRun: 1})
	assert.Empty(t, engine.Snapshot("run"))
	engine.ClearRun("run")
	engine.ClearRun("")
	engine.RemoveReminder("unknown", "one")
	engine.RemoveReminder("", "")
	engine.AddReminder("", reminder.Reminder{ID: "ignored", Text: "ignored"})
	engine.AddReminder("run", reminder.Reminder{ID: "one", Text: "replacement", Priority: reminder.TierGuidance, MaxPerRun: 1})
	snapshot := engine.Snapshot("run")
	require.Len(t, snapshot, 1)
	assert.Equal(t, "replacement", snapshot[0].Text)
	assert.Empty(t, engine.Snapshot(""))
}
