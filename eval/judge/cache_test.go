package judge

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaches(t *testing.T) {
	dir := t.TempDir()
	disk, err := NewDirCache(dir)
	require.NoError(t, err)
	caches := []struct {
		name  string
		cache Cache
	}{{"memory", NewMemoryCache()}, {"directory", disk}}
	for _, tt := range caches {
		t.Run(tt.name, func(t *testing.T) {
			want := &Calibration{Key: "sha256:" + strings.Repeat("a", 64), ModelID: "model", PromptHash: "hash", Passed: true, Completed: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), Examples: []ExampleResult{{ID: "one", Want: map[string]Label{"a": Entailed}, Got: map[string]Label{"a": Entailed}}}}
			require.NoError(t, tt.cache.Store(want))
			want.Examples[0].Got["a"] = Contradicted
			got, ok := tt.cache.Load(want.Key)
			require.True(t, ok)
			assert.Equal(t, Entailed, got.Examples[0].Got["a"])
			got.Examples[0].Got["a"] = NotAddressed
			reloaded, ok := tt.cache.Load(want.Key)
			require.True(t, ok)
			assert.Equal(t, Entailed, reloaded.Examples[0].Got["a"])
			want.Examples[0].Got["a"] = Entailed
			assert.Equal(t, want, reloaded)
		})
	}
	second, err := NewDirCache(dir)
	require.NoError(t, err)
	key := "sha256:" + strings.Repeat("a", 64)
	got, ok := second.Load(key)
	require.True(t, ok)
	assert.True(t, got.Passed)
	path := filepath.Join(dir, strings.Repeat("a", 64)+".json")
	require.NoError(t, os.WriteFile(path, []byte("broken"), 0600))
	_, ok = second.Load(key)
	assert.False(t, ok)
	require.NoError(t, os.WriteFile(path, []byte(`{}`), 0600))
	_, ok = second.Load(key)
	assert.False(t, ok)
}

func TestCacheRejectsIncompleteCalibration(t *testing.T) {
	disk, err := NewDirCache(t.TempDir())
	require.NoError(t, err)
	for _, cache := range []Cache{NewMemoryCache(), disk} {
		err := cache.Store(&Calibration{Key: "sha256:" + strings.Repeat("b", 64)})
		assert.Error(t, err)
	}
}

func TestDirCacheAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewDirCache(dir)
	require.NoError(t, err)
	old := &Calibration{Key: "sha256:" + strings.Repeat("c", 64), ModelID: "old", Passed: true, Examples: []ExampleResult{}, Completed: time.Now().UTC()}
	require.NoError(t, cache.Store(old))
	path := cache.path(old.Key)
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, root.Close())
	}()
	before, err := root.ReadFile(filepath.Base(path))
	require.NoError(t, err)
	reader, err := root.Open(filepath.Base(path))
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, reader.Close())
	}()
	next := *old
	next.ModelID = "replacement"
	require.NoError(t, cache.Store(&next))
	// A reader holding the old inode must see its complete original value even
	// when it reads only after replacement; truncating in place fails this proof.
	held, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, before, held)
	current, ok := cache.Load(old.Key)
	require.True(t, ok)
	assert.Equal(t, &next, current)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, filepath.Base(path), entries[0].Name())
}

func TestDirCacheFailedRenameCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewDirCache(dir)
	require.NoError(t, err)
	value := &Calibration{Key: "sha256:" + strings.Repeat("d", 64), Completed: time.Now().UTC()}
	path := cache.path(value.Key)
	require.NoError(t, os.Mkdir(path, 0700))
	marker := filepath.Join(path, "keep")
	require.NoError(t, os.WriteFile(marker, []byte("untouched"), 0600))
	require.Error(t, cache.Store(value))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, root.Close())
	}()
	data, err := root.ReadFile(filepath.Join(filepath.Base(path), "keep"))
	require.NoError(t, err)
	assert.Equal(t, "untouched", string(data))
}
