package inmem

import (
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceOwnsNestedMetadata(t *testing.T) {
	service := NewService()
	scope := memory.Scope{Namespace: "workspace", UserID: "user", Visibility: memory.VisibilityUser}
	metadata := map[string]any{"items": []any{map[string]any{"value": "original"}, []byte("original")}}
	entry, err := service.PutEntry(t.Context(), memory.PutEntryInput{
		Scope: scope, Content: "remember metadata", Author: "user", Metadata: metadata,
	})
	require.NoError(t, err)
	metadata["items"].([]any)[0].(map[string]any)["value"] = "input"
	assert.Equal(t, "original", entry.Metadata["items"].([]any)[0].(map[string]any)["value"])
	entry.Metadata["items"].([]any)[1].([]byte)[0] = 'x'
	query := memory.SearchQuery{Scope: scope, Query: "metadata"}
	result, err := service.Search(t.Context(), query)
	require.NoError(t, err)
	require.Len(t, result.Hits, 1)
	assert.Equal(t, "original", result.Hits[0].Entry.Metadata["items"].([]any)[0].(map[string]any)["value"])
	assert.Equal(t, []byte("original"), result.Hits[0].Entry.Metadata["items"].([]any)[1])
	result.Hits[0].Entry.Metadata["items"].([]any)[0] = "output"
	again, err := service.Search(t.Context(), query)
	require.NoError(t, err)
	require.Len(t, again.Hits, 1)
	assert.Equal(t, "original", again.Hits[0].Entry.Metadata["items"].([]any)[0].(map[string]any)["value"])
}
