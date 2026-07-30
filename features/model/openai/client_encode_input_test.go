package openai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

func TestEncodeInput_PartSkipContract(t *testing.T) {
	tests := []struct {
		name      string
		messages  []*model.Message
		wantItems int
		wantText  string
		wantErr   string
	}{
		{
			name: "user cache checkpoint is ignored",
			messages: []*model.Message{
				{
					Role: model.ConversationRoleUser,
					Parts: []model.Part{
						model.TextPart{Text: "hi"},
						model.CacheCheckpointPart{},
					},
				},
			},
			wantItems: 1,
			wantText:  "hi",
		},
		{
			name: "system cache checkpoint is ignored",
			messages: []*model.Message{
				{
					Role: model.ConversationRoleSystem,
					Parts: []model.Part{
						model.TextPart{Text: "be brief"},
						model.CacheCheckpointPart{},
					},
				},
			},
			wantItems: 1,
			wantText:  "be brief",
		},
		{
			name: "replayed signed thinking is dropped",
			messages: []*model.Message{
				{
					Role: model.ConversationRoleAssistant,
					Parts: []model.Part{
						model.ThinkingPart{Text: "private reasoning", Signature: "sig"},
						model.TextPart{Text: "answer"},
					},
				},
			},
			wantItems: 1,
			wantText:  "answer",
		},
		{
			name: "thinking-only message yields no items",
			messages: []*model.Message{
				{
					Role: model.ConversationRoleAssistant,
					Parts: []model.Part{
						model.ThinkingPart{Text: "private reasoning", Signature: "sig"},
					},
				},
			},
			wantItems: 0,
		},
		{
			name: "unsupported part still errors",
			messages: []*model.Message{
				{
					Role: model.ConversationRoleUser,
					Parts: []model.Part{
						model.CacheCheckpointPart{},
						model.DocumentPart{Name: "spec", Format: model.DocumentFormatTXT, Text: "hello"},
					},
				},
			},
			wantErr: "openai responses: unsupported message part model.DocumentPart",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := encodeInput(tt.messages, nil)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Len(t, items, tt.wantItems)
			if tt.wantText != "" {
				msg := items[0].OfMessage
				require.NotNil(t, msg)
				assert.Equal(t, tt.wantText, msg.Content.OfString.Value)
			}
		})
	}
}
