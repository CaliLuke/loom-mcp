package localevidence

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/runstoretest"
)

func TestRunStoreConformance(t *testing.T) {
	runstoretest.RunConformance(t, func(t *testing.T) runstore.Store {
		s, err := Create(t.Context(), t.TempDir())
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, s.Close())
		})
		return s
	})
}

func TestRunLogRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Create(t.Context(), dir)
	require.NoError(t, err)
	event := &runlog.Event{RunID: "run", EventKey: "first", AgentID: "agent", Type: hooks.RunStarted, Timestamp: time.Unix(2, 3).UTC(), Payload: rawjson.Message(` {"n":9007199254740993} `)}
	first, err := s.Append(t.Context(), event)
	require.NoError(t, err)
	assert.True(t, first.Inserted)
	duplicate, err := s.Append(t.Context(), event)
	require.NoError(t, err)
	assert.False(t, duplicate.Inserted)
	assert.Equal(t, first.ID, duplicate.ID)
	conflict := *event
	conflict.Payload = rawjson.Message(`{}`)
	_, err = s.Append(t.Context(), &conflict)
	require.ErrorContains(t, err, "conflict")
	second := *event
	second.EventKey = "second"
	_, err = s.Append(t.Context(), &second)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	s, err = Open(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, s.Close())
	})
	page, err := s.List(t.Context(), "run", "", 1)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	assert.Equal(t, event, page.Events[0])
	require.NotEmpty(t, page.NextCursor)
	page, err = s.List(t.Context(), "run", page.NextCursor, 1)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	assert.Equal(t, "second", page.Events[0].EventKey)
	assert.Empty(t, page.NextCursor)
	for _, payload := range []rawjson.Message{nil, {}} {
		empty := &runlog.Event{RunID: "run", EventKey: "empty", Payload: payload}
		_, err := s.Append(t.Context(), empty)
		require.NoError(t, err, "empty and nil payloads have identical bytes")
	}
}

func TestTwoOpenSequenceCounters(t *testing.T) {
	dir := t.TempDir()
	a, err := Create(t.Context(), dir)
	require.NoError(t, err)
	b, err := Open(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, a.Close())
		require.NoError(t, b.Close())
	})
	results := make(chan int, 40)
	var workers sync.WaitGroup
	for _, store := range []*Store{a, b} {
		workers.Go(func() {
			for range 20 {
				n, err := store.NextSeq(t.Context(), "root", "child")
				if assert.NoError(t, err) {
					results <- n
				}
			}
		})
	}
	workers.Wait()
	close(results)
	var values []int
	for n := range results {
		values = append(values, n)
	}
	slices.Sort(values)
	require.Len(t, values, 40)
	for i, n := range values {
		assert.Equal(t, i+1, n)
	}
	roots, err := a.Roots(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"root"}, roots)
	runs, err := b.RunIDs(t.Context(), "root")
	require.NoError(t, err)
	assert.Equal(t, []string{"child"}, runs)
}

func TestStoreErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(t.Context(), dir)
	require.Error(t, err)
	s, err := Create(t.Context(), dir)
	require.NoError(t, err)
	_, err = Create(t.Context(), dir)
	require.ErrorIs(t, err, os.ErrExist)
	require.NoError(t, s.Close())
	_, err = s.Put(t.Context(), "root", []byte("no"))
	require.Error(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, FileName), []byte("corrupted database"), 0600))
	_, err = Open(t.Context(), dir)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = Create(ctx, t.TempDir())
	require.ErrorIs(t, err, context.Canceled)
}
