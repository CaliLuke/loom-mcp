package runtime

import (
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Canonical JSON is the planner-visible equality criterion, including for the
// in-memory engine. Differences erased by the message codec are unobservable.
func TestIndexCanonicalMessageEquality(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    *model.Message
		b    *model.Message
	}{
		{"nil versus empty parts", &model.Message{Role: model.ConversationRoleUser}, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{}}},
		{"pointer versus value part", &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{&model.TextPart{Text: "text"}}}, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "text"}}}},
		{"raw versus semantic tool arguments", &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "id", Name: "tool", Input: rawjson.Message(`{"n":1}`)}}}, &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "id", Name: "tool", Input: map[string]any{"n": float64(1)}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := encodeContentMessages([]*model.Message{tc.a, tc.b})
			require.NoError(t, err)
			assert.NotEqual(t, tc.a, tc.b)
			assert.Equal(t, encoded[0], encoded[1], "K6 already erases the representation distinction")
			store := inmem.New()
			hash, err := store.Put(t.Context(), "root", encoded[0])
			require.NoError(t, err)
			got, err := index.Render(t.Context(), []index.MessageEntry{{ID: 1, Kind: index.Message, Hash: hash}}, func(ctx context.Context, hash runstore.Hash) ([]byte, error) {
				return store.Get(ctx, "root", hash)
			})
			require.NoError(t, err)
			reencoded, err := json.Marshal(got[0])
			require.NoError(t, err)
			assert.Equal(t, encoded[0], reencoded, "stored wire representation is preserved")
		})
	}
}
