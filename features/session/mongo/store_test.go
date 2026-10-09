package mongo

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	sessioninmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type inMemorySessionClient struct {
	*sessioninmem.Store
}

func (c *inMemorySessionClient) Name() string {
	return "test-session-client"
}

func (c *inMemorySessionClient) Ping(context.Context) error {
	return nil
}

func TestNewStoreRequiresClient(t *testing.T) {
	_, err := NewStore(nil)
	require.EqualError(t, err, "client is required")
}

func TestStoreSessionLifecycle(t *testing.T) {
	client := &inMemorySessionClient{Store: sessioninmem.New()}
	store, err := NewStore(client)
	require.NoError(t, err)
	ctx := t.Context()
	now := time.Unix(1, 0).UTC()
	created, err := store.CreateSession(ctx, "session", now)
	require.NoError(t, err)
	assert.Equal(t, session.StatusActive, created.Status)
	assert.Equal(t, now, created.CreatedAt)
	loaded, err := store.LoadSession(ctx, "session")
	require.NoError(t, err)
	assert.Equal(t, created, loaded)
	end := now.Add(time.Hour)
	ended, err := store.EndSession(ctx, "session", end)
	require.NoError(t, err)
	assert.Equal(t, session.StatusEnded, ended.Status)
	require.NotNil(t, ended.EndedAt)
	assert.Equal(t, end, *ended.EndedAt)
	_, err = store.LoadSession(ctx, "missing")
	assert.ErrorIs(t, err, session.ErrSessionNotFound)
}

func TestStoreRunAdmissionAndLinking(t *testing.T) {
	client := &inMemorySessionClient{Store: sessioninmem.New()}
	store, err := NewStore(client)
	require.NoError(t, err)
	ctx := t.Context()
	_, err = store.CreateSession(ctx, "session", time.Unix(1, 0))
	require.NoError(t, err)
	parent := session.RunMeta{RunID: "parent", AgentID: "agent", SessionID: "session", Status: session.RunStatusRunning}
	require.NoError(t, store.UpsertRun(ctx, parent))
	child := session.RunMeta{RunID: "child", AgentID: "child-agent", SessionID: "session", Status: session.RunStatusPending}
	require.NoError(t, store.ReserveRun(ctx, child, "attempt", "parent"))
	require.NoError(t, store.LinkChildRun(ctx, parent.RunID, child))
	loadedParent, err := store.LoadRun(ctx, parent.RunID)
	require.NoError(t, err)
	assert.Equal(t, []string{child.RunID}, loadedParent.ChildRunIDs)
	pending, err := store.ListRunsBySession(ctx, "session", []session.RunStatus{session.RunStatusPending})
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, child.RunID, pending[0].RunID)
	require.NoError(t, store.RejectRun(ctx, child.RunID, "attempt"))
	stored, err := store.LoadRun(ctx, child.RunID)
	require.NoError(t, err)
	assert.Equal(t, session.RunStatusFailed, stored.Status)
	assert.ErrorIs(t, store.ReserveRun(ctx, child, "other", "parent"), session.ErrRunAlreadyExists)
}
