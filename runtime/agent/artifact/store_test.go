package artifact

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemoryStoreSaveListLoadAndFilter(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	ref, err := store.Save(ctx, SaveInput{
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "call-1",
		Name:       "report.txt",
		MimeType:   "text/plain",
		Metadata:   map[string]string{"kind": "report", "tenant": "acme"},
		Body:       []byte("hello world"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, ref.ID)
	require.Equal(t, int64(11), ref.SizeBytes)

	_, err = store.Save(ctx, SaveInput{
		AgentID:    "svc.agent",
		RunID:      "run-1",
		ToolCallID: "call-2",
		Name:       "table.json",
		MimeType:   "application/json",
		Metadata:   map[string]string{"kind": "data", "tenant": "acme"},
		Body:       []byte(`{"ok":true}`),
	})
	require.NoError(t, err)

	refs, err := store.List(ctx, ListQuery{
		AgentID:  "svc.agent",
		RunID:    "run-1",
		MimeType: "text/plain",
		Metadata: map[string]string{"kind": "report"},
		Limit:    10,
	})
	require.NoError(t, err)
	require.Len(t, refs, 1)
	require.Equal(t, ref.ID, refs[0].ID)

	content, err := store.Load(ctx, LoadQuery{
		AgentID:  "svc.agent",
		RunID:    "run-1",
		ID:       ref.ID,
		MaxBytes: 5,
	})
	require.NoError(t, err)
	require.Equal(t, []byte("hello"), content.Body)
	require.True(t, content.Truncated)
	require.Equal(t, int64(11), content.SizeBytes)
	require.Equal(t, "text/plain", content.Ref.MimeType)
}

func TestMemoryStoreIsolatesByAgentAndRun(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	ref, err := store.Save(ctx, SaveInput{
		AgentID:  "svc.agent",
		RunID:    "run-1",
		Name:     "report.txt",
		MimeType: "text/plain",
		Body:     []byte("hello world"),
	})
	require.NoError(t, err)

	for _, query := range []LoadQuery{
		{AgentID: "other.agent", RunID: "run-1", ID: ref.ID},
		{AgentID: "svc.agent", RunID: "run-2", ID: ref.ID},
	} {
		_, err := store.Load(ctx, query)
		require.ErrorIs(t, err, ErrNotFound)
	}
}

func TestMemoryStoreReportsMissingArtifacts(t *testing.T) {
	_, err := NewMemoryStore().Load(context.Background(), LoadQuery{
		AgentID: "svc.agent",
		RunID:   "run-1",
		ID:      "missing",
	})
	require.ErrorIs(t, err, ErrNotFound)
}
