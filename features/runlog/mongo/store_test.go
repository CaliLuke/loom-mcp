package mongo

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/runtime/agent/runlog"
)

func TestStoreRequiresClientAndDelegatesContracts(t *testing.T) {
	_, err := NewStore(nil)
	require.EqualError(t, err, "client is required")

	wantErr := errors.New("backend failed")
	client := &recordingClient{
		appendResult: runlog.AppendResult{ID: "event-1", Inserted: true},
		page:         runlog.Page{NextCursor: "event-2"},
	}
	store, err := NewStore(client)
	require.NoError(t, err)
	event := &runlog.Event{RunID: "run-1", EventKey: "key-1"}
	result, err := store.Append(context.Background(), event)
	require.NoError(t, err)
	assert.Equal(t, client.appendResult, result)
	assert.Same(t, event, client.event)

	page, err := store.List(context.Background(), "run-1", "event-1", 25)
	require.NoError(t, err)
	assert.Equal(t, client.page, page)
	assert.Equal(t, "run-1", client.runID)
	assert.Equal(t, "event-1", client.cursor)
	assert.Equal(t, 25, client.limit)

	client.err = wantErr
	_, err = store.Append(context.Background(), event)
	require.ErrorIs(t, err, wantErr)
	_, err = store.List(context.Background(), "run-1", "", 1)
	require.ErrorIs(t, err, wantErr)
}

type recordingClient struct {
	appendResult runlog.AppendResult
	page         runlog.Page
	event        *runlog.Event
	runID        string
	cursor       string
	limit        int
	err          error
}

func (c *recordingClient) Name() string {
	return "recording"
}

func (c *recordingClient) Ping(context.Context) error {
	return c.err
}

func (c *recordingClient) Append(_ context.Context, event *runlog.Event) (runlog.AppendResult, error) {
	c.event = event
	return c.appendResult, c.err
}

func (c *recordingClient) List(_ context.Context, runID string, cursor string, limit int) (runlog.Page, error) {
	c.runID = runID
	c.cursor = cursor
	c.limit = limit
	return c.page, c.err
}
