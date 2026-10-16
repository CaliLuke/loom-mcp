package evidence_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/evidence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFreshCaptureAndReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "capture")
	store, err := evidence.Create(t.Context(), dir)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	_, err = evidence.Create(t.Context(), dir)
	require.ErrorIs(t, err, os.ErrExist)

	store, err = evidence.Open(t.Context(), dir)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	snapshot, err := evidence.Read(t.Context(), dir)
	require.NoError(t, err)
	assert.Empty(t, snapshot.Records)
	assert.Empty(t, snapshot.Unlinked)
}

func TestOpenRejectsMissingAndCorruptCapture(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	_, err := evidence.Open(t.Context(), dir)
	require.Error(t, err)
	_, err = evidence.Read(t.Context(), dir)
	require.Error(t, err)

	dir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "evidence.sqlite"), []byte("bad database"), 0600))
	_, err = evidence.Open(t.Context(), dir)
	require.Error(t, err)
	assert.NotErrorIs(t, err, os.ErrNotExist)
}
