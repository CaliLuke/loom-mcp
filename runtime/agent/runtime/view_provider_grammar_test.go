package runtime

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompactedViewPreservesAdjacentAssistantMessages(t *testing.T) {
	first := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ThinkingPart{Redacted: []byte("provider-issued")}}}
	second := &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "answer"}}}
	for _, purpose := range []viewLoadPurpose{plannerView, childAdmissionView} {
		cache := runViewCache{
			checkpointMessage: &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "checkpoint"}}},
			tailMessages:      []*model.Message{first, second},
		}
		messages, err := cache.assembleView(purpose)
		require.NoError(t, err)
		require.Len(t, messages, 3)
		assert.Equal(t, first, messages[1])
		assert.Equal(t, second, messages[2])
		assert.ErrorContains(t, transcript.ValidateView(messages, transcript.GrammarBedrock{}), "repeats role")
	}
}
