package recall

import (
	"context"
	"encoding/json/v2"
	"errors"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCurrentRunCitationResolver(t *testing.T) {
	data, err := json.Marshal(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "canonical first line\nsecond line"}}})
	require.NoError(t, err)
	result, err := json.Marshal(&model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: "fact"}}})
	require.NoError(t, err)
	reads := 0
	source := func(_ context.Context, hash runstore.Hash) ([]byte, error) {
		reads++
		if hash == "user" {
			return data, nil
		}
		if hash == "result" {
			return result, nil
		}
		return nil, errors.New("unexpected content read")
	}
	entries := []index.MessageEntry{{ID: 1, Kind: index.Message, Role: model.ConversationRoleUser, Hash: "user"}, {ID: 2, Kind: index.Assistant}, {ID: 3, Kind: index.Message, Role: model.ConversationRoleUser, Hash: "result"}, {ID: 4, Kind: index.ToolResult}}
	resolver := NewCitationResolver(t.Context(), source, entries, 3)
	for _, test := range []struct {
		key           checkpoint.OrderKey
		found, behind bool
		kind          index.EntryKind
	}{
		{checkpoint.OrderKey{ID: 1}, true, true, index.Message},
		{checkpoint.OrderKey{ID: 2}, true, true, index.Assistant},
		{checkpoint.OrderKey{ID: 3}, true, true, index.ToolResult},
		{checkpoint.OrderKey{ID: 4}, true, false, index.ToolResult},
		{checkpoint.OrderKey{ID: 5}, false, false, ""},
		{checkpoint.OrderKey{Pos: 2, ID: 1}, false, false, ""},
	} {
		target, found, err := resolver.Resolve(test.key)
		require.NoError(t, err)
		assert.Equal(t, test.found, found)
		assert.Equal(t, test.behind, resolver.Behind(test.key))
		if found {
			assert.Equal(t, test.kind, target.Kind)
		}
		if test.key == (checkpoint.OrderKey{ID: 1}) {
			assert.Equal(t, "canonical first line", target.Line.Text)
		}
	}
	assert.Equal(t, 2, reads)
}
