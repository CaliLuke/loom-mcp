package judge

import (
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
