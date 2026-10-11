package contextmeasure

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestViewBearingCanonicalMessages(t *testing.T) {
	original := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "original"}, model.CacheCheckpointPart{}}, Meta: map[string]any{"id": "old"}}
	for _, tc := range []struct {
		name    string
		message *model.Message
		want    bool
	}{
		{"changed_meta_cache_and_pointer", &model.Message{Role: original.Role, Parts: []model.Part{&model.CacheCheckpointPart{}, &model.TextPart{Text: "original"}}, Meta: map[string]any{"id": "new"}}, true},
		{"missing_meta", &model.Message{Role: original.Role, Parts: []model.Part{model.TextPart{Text: "original"}}}, true},
		{"rewritten", &model.Message{Role: original.Role, Parts: []model.Part{model.TextPart{Text: "rewritten"}}}, false},
		{"changed_role", &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "original"}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ViewBearing(&model.Request{Messages: []*model.Message{tc.message}}, []*model.Message{original})
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestViewBearingCanonicalJSON(t *testing.T) {
	view := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "id", Content: map[string]any{"b": 2, "a": 1}}}}
	match := &model.Message{Role: view.Role, Parts: []model.Part{&model.ToolResultPart{ToolUseID: "id", Content: map[string]any{"a": 1, "b": 2}}}}
	for i := 0; i < 100; i++ {
		got, err := ViewBearing(&model.Request{Messages: []*model.Message{match}}, []*model.Message{view})
		require.NoError(t, err)
		assert.True(t, got, "unstable canonical JSON")
	}
	view.Parts = []model.Part{model.ToolResultPart{ToolUseID: "id", Content: uint64(9007199254740992)}}
	match.Parts = []model.Part{model.ToolResultPart{ToolUseID: "id", Content: uint64(9007199254740993)}}
	got, err := ViewBearing(&model.Request{Messages: []*model.Message{match}}, []*model.Message{view})
	require.NoError(t, err)
	assert.False(t, got, "canonicalization lost integer precision")
}
