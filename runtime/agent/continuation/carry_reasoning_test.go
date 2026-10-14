package continuation

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCarryOmitsReasoningOnlyMessages(t *testing.T) {
	reasoning := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ThinkingPart{Redacted: []byte("provider-issued")}}}
	text := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "answer"}}}
	for _, messages := range [][]*model.Message{{reasoning, text}, {reasoning}} {
		plan, err := Carry([]Entry{{Entry: index.MessageEntry{ID: 1, Kind: index.Assistant, Role: model.ConversationRoleAssistant}, Messages: messages}}, index.ViewRef{}, 10000, 100)
		require.NoError(t, err)
		require.Len(t, plan.Tail, 1, "preserve canonical entry provenance")
		assert.Equal(t, messages[1:], plan.Tail[0].Messages)
		assert.Len(t, reasoning.Parts, 1, "source evidence remains intact")
	}
}
