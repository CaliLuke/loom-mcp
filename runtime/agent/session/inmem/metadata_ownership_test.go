package inmem

import (
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreOwnsNestedMetadataAndEmptyLabels(t *testing.T) {
	store := New()
	_, err := store.CreateSession(t.Context(), "session", time.Unix(1, 0))
	require.NoError(t, err)
	input := session.RunMeta{
		AgentID: "agent", RunID: "run", SessionID: "session", Status: session.RunStatusRunning,
		Labels:   map[string]string{},
		Metadata: map[string]any{"items": []any{map[string]any{"value": "original"}, []byte("original")}},
	}
	require.NoError(t, store.UpsertRun(t.Context(), input))
	input.Labels["added"] = "input"
	input.Metadata["items"].([]any)[0].(map[string]any)["value"] = "input"
	input.Metadata["items"].([]any)[1].([]byte)[0] = 'x'
	loaded, err := store.LoadRun(t.Context(), "run")
	require.NoError(t, err)
	assert.Empty(t, loaded.Labels)
	assert.Equal(t, "original", loaded.Metadata["items"].([]any)[0].(map[string]any)["value"])
	assert.Equal(t, []byte("original"), loaded.Metadata["items"].([]any)[1])
	loaded.Labels["added"] = "loaded"
	loaded.Metadata["items"].([]any)[0] = "loaded"
	listed, err := store.ListRunsBySession(t.Context(), "session", nil)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Empty(t, listed[0].Labels)
	assert.Equal(t, "original", listed[0].Metadata["items"].([]any)[0].(map[string]any)["value"])
	listed[0].Metadata["items"].([]any)[1].([]byte)[0] = 'x'
	again, err := store.LoadRun(t.Context(), "run")
	require.NoError(t, err)
	assert.Equal(t, []byte("original"), again.Metadata["items"].([]any)[1])
}
