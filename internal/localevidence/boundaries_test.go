package localevidence

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

func TestClosedCaptureRejectsOperations(t *testing.T) {
	store, err := Create(t.Context(), t.TempDir())
	require.NoError(t, err)
	require.NoError(t, store.Close())
	for _, test := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"content read", func(ctx context.Context) error {
			data, err := store.Get(ctx, "root", "sha256:missing")
			assert.Nil(t, data)
			return err
		}},
		{"call write", func(ctx context.Context) error {
			return store.PutCall(ctx, "root", boundaryCall())
		}},
		{"call read", func(ctx context.Context) error {
			records, err := store.ListCalls(ctx, "root", "run")
			assert.Empty(t, records)
			return err
		}},
		{"scope deletion", func(ctx context.Context) error {
			return store.DeleteScope(ctx, "root")
		}},
		{"event append", func(ctx context.Context) error {
			event := &runlog.Event{RunID: "run", EventKey: "event"}
			result, err := store.Append(ctx, event)
			assert.Empty(t, result.ID)
			assert.False(t, result.Inserted)
			assert.Empty(t, event.ID)
			return err
		}},
		{"event read", func(ctx context.Context) error {
			page, err := store.List(ctx, "run", "", 1)
			assert.Empty(t, page.Events)
			return err
		}},
		{"root discovery", func(ctx context.Context) error {
			roots, err := store.Roots(ctx)
			assert.Empty(t, roots)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorContains(t, test.run(t.Context()), "database is closed")
		})
	}
}

func TestEventValidationLeavesCaptureEmpty(t *testing.T) {
	store := boundaryStore(t)
	for _, event := range []*runlog.Event{nil, {EventKey: "key"}, {RunID: "run"}} {
		result, err := store.Append(t.Context(), event)
		require.ErrorContains(t, err, "required")
		assert.False(t, result.Inserted)
		assert.Empty(t, result.ID)
	}
	for _, test := range []struct {
		runID  string
		cursor string
		limit  int
	}{
		{"", "", 1}, {"run", "", 0}, {"run", "", -1},
		{"run", "invalid", 1}, {"run", "0", 1}, {"run", "-1", 1},
	} {
		_, err := store.List(t.Context(), test.runID, test.cursor, test.limit)
		require.Error(t, err)
	}
	page, err := store.List(t.Context(), "run", "", 1)
	require.NoError(t, err)
	assert.Empty(t, page.Events)
	require.ErrorContains(t, store.RegisterRoot(t.Context(), ""), "scope is required")
	roots, err := store.Roots(t.Context())
	require.NoError(t, err)
	assert.Empty(t, roots)
}

func TestCorruptCaptureRowsAreRejected(t *testing.T) {
	t.Run("content address", func(t *testing.T) {
		store := boundaryStore(t)
		hash, err := store.Put(t.Context(), "root", []byte("original"))
		require.NoError(t, err)
		_, err = store.db.ExecContext(t.Context(), "UPDATE content SET data=?", []byte("tampered"))
		require.NoError(t, err)
		data, err := store.Get(t.Context(), "root", hash)
		require.ErrorContains(t, err, "corrupt content")
		assert.Nil(t, data)
	})
	t.Run("call lifecycle", func(t *testing.T) {
		store := boundaryStore(t)
		require.NoError(t, store.PutCall(t.Context(), "root", boundaryCall()))
		_, err := store.db.ExecContext(t.Context(), "UPDATE calls SET state='completed'")
		require.NoError(t, err)
		records, err := store.ListCalls(t.Context(), "root", "run")
		require.ErrorContains(t, err, "requires a completion body")
		assert.Empty(t, records)
	})
	t.Run("event encoding", func(t *testing.T) {
		store := boundaryStore(t)
		_, err := store.Append(t.Context(), &runlog.Event{RunID: "run", EventKey: "key"})
		require.NoError(t, err)
		_, err = store.db.ExecContext(t.Context(), "UPDATE events SET body=?", []byte("invalid JSON"))
		require.NoError(t, err)
		page, err := store.List(t.Context(), "run", "", 1)
		require.ErrorContains(t, err, "corrupt event")
		assert.Empty(t, page.Events)
	})
}

func TestCaptureOwnershipRejectsInvalidLocationsAndVersions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "occupied")
	require.NoError(t, os.WriteFile(path, []byte("existing owner"), 0600))
	store, err := Create(t.Context(), path)
	require.ErrorContains(t, err, "not a directory")
	assert.Nil(t, store)
	store, err = Create(t.Context(), filepath.Join(path, "capture"))
	require.Error(t, err)
	assert.Nil(t, store)
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	data, err := root.ReadFile("occupied")
	require.NoError(t, err)
	require.NoError(t, root.Close())
	assert.Equal(t, "existing owner", string(data))

	dir = t.TempDir()
	store, err = Create(t.Context(), dir)
	require.NoError(t, err)
	_, err = store.db.ExecContext(t.Context(), "PRAGMA user_version=2")
	require.NoError(t, err)
	require.NoError(t, store.Close())
	store, err = Open(t.Context(), dir)
	require.ErrorContains(t, err, "unsupported database version 2")
	assert.Nil(t, store)
}

func TestRejectedCaptureWritesAreAtomic(t *testing.T) {
	t.Run("call", func(t *testing.T) {
		store := boundaryStore(t)
		_, err := store.db.ExecContext(t.Context(), "CREATE TRIGGER reject_call BEFORE INSERT ON calls BEGIN SELECT RAISE(ABORT,'call rejected'); END")
		require.NoError(t, err)
		require.ErrorContains(t, store.PutCall(t.Context(), "root", boundaryCall()), "call rejected")
		records, err := store.ListCalls(t.Context(), "root", "run")
		require.NoError(t, err)
		assert.Empty(t, records)
	})
	t.Run("event", func(t *testing.T) {
		store := boundaryStore(t)
		_, err := store.db.ExecContext(t.Context(), "CREATE TRIGGER reject_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'event rejected'); END")
		require.NoError(t, err)
		event := &runlog.Event{RunID: "run", EventKey: "key"}
		result, err := store.Append(t.Context(), event)
		require.ErrorContains(t, err, "event rejected")
		assert.False(t, result.Inserted)
		assert.Empty(t, result.ID)
		assert.Empty(t, event.ID)
		page, err := store.List(t.Context(), "run", "", 1)
		require.NoError(t, err)
		assert.Empty(t, page.Events)
	})
	t.Run("scope deletion", func(t *testing.T) {
		store := boundaryStore(t)
		hash, err := store.Put(t.Context(), "root", []byte("preserved"))
		require.NoError(t, err)
		require.NoError(t, store.PutCall(t.Context(), "root", boundaryCall()))
		_, err = store.db.ExecContext(t.Context(), "CREATE TRIGGER reject_delete BEFORE DELETE ON calls BEGIN SELECT RAISE(ABORT,'delete rejected'); END")
		require.NoError(t, err)
		require.ErrorContains(t, store.DeleteScope(t.Context(), "root"), "delete rejected")
		data, err := store.Get(t.Context(), "root", hash)
		require.NoError(t, err)
		assert.Equal(t, []byte("preserved"), data)
		records, err := store.ListCalls(t.Context(), "root", "run")
		require.NoError(t, err)
		assert.Equal(t, []runstore.CallRecord{boundaryCall()}, records)
	})
}

func boundaryStore(t *testing.T) *Store {
	t.Helper()
	store, err := Create(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})
	return store
}

func boundaryCall() runstore.CallRecord {
	return runstore.CallRecord{Key: runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}, State: runstore.CallStarted, Started: []byte("request")}
}
