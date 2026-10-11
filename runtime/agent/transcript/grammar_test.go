package transcript

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateView checks both provider grammars and complete tool handshakes.
func TestValidateView(t *testing.T) {
	user := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "task"}}}
	assistant := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "answer"}}}
	call := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ThinkingPart{Text: "opaque", Signature: "sig"}, model.ToolUsePart{ID: "a"}, model.ToolUsePart{ID: "b"}}}
	result := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "a"}, model.ToolResultPart{ToolUseID: "b"}}}
	system := &model.Message{Role: model.ConversationRoleSystem}
	checkpoint := &model.Message{Role: model.ConversationRoleUser, Meta: map[string]any{CheckpointMetaKey: true}}
	tests := []struct {
		name               string
		messages           []*model.Message
		bedrock, anthropic bool
	}{
		{"history", []*model.Message{system, user, assistant}, true, true},
		{"checkpoint", []*model.Message{checkpoint, assistant}, true, true},
		{"thinking tail", []*model.Message{checkpoint, call, system, result, assistant}, true, true},
		{"user tail", []*model.Message{checkpoint, user}, false, false},
		{"orphan result", []*model.Message{user, assistant, result}, false, false},
		{"unanswered call", []*model.Message{user, call}, false, false},
		{"pointer call without result", []*model.Message{user, {Role: model.ConversationRoleAssistant, Parts: []model.Part{&model.ThinkingPart{Text: "intact"}, &model.ToolUsePart{ID: "a"}}}}, false, false},
		{"partial batch", []*model.Message{user, call, {Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "a"}}}}, false, false},
		{"first assistant", []*model.Message{assistant}, false, false},
		{"consecutive users", []*model.Message{user, user}, false, true},
		{"duplicate result", []*model.Message{user, call, {Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "a"}, model.ToolResultPart{ToolUseID: "a"}}}}, false, false},
		{"wrong role call", []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolUsePart{ID: "a"}}}}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.bedrock, ValidateView(tt.messages, GrammarBedrock{Thinking: true}) == nil)
			assert.Equal(t, tt.anthropic, ValidateView(tt.messages, GrammarAnthropic{}) == nil)
		})
	}
	noThinking := &model.Message{Role: model.ConversationRoleAssistant, Parts: call.Parts[1:]}
	require.Error(t, ValidateView([]*model.Message{user, noThinking, result}, GrammarBedrock{Thinking: true}))
	assert.NoError(t, ValidateView([]*model.Message{user, noThinking, result}, GrammarBedrock{}))
}

// TestAssembleView preserves tail reasoning and strips pre-tail reasoning without mutation.
func TestAssembleView(t *testing.T) {
	thought := model.ThinkingPart{Text: "signed", Signature: "sig", Redacted: []byte{1}}
	system := &model.Message{Role: model.ConversationRoleSystem, Parts: []model.Part{thought, model.TextPart{Text: "system"}}}
	checkpoint := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{thought, model.TextPart{Text: "checkpoint"}}}
	tail := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{thought, model.TextPart{Text: "tail"}}}
	got := AssembleView([]*model.Message{system}, checkpoint, []*model.Message{tail})
	assert.Len(t, got[0].Parts, 1)
	assert.Len(t, got[1].Parts, 1)
	assert.Equal(t, tail, got[2])
	assert.Len(t, system.Parts, 2)
	assert.Len(t, checkpoint.Parts, 2)
	assert.NoError(t, ValidateView(got, GrammarBedrock{Thinking: true}))
}
