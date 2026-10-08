package registry

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryCacheRefreshPreservesConcurrentMutation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*testing.T, *MemoryCache)
		want   *ToolsetSchema
	}{
		{name: "set", mutate: func(t *testing.T, c *MemoryCache) {
			require.NoError(t, c.Set(t.Context(), "k", &ToolsetSchema{ID: "new"}, time.Hour))
		}, want: &ToolsetSchema{ID: "new"}},
		{name: "delete", mutate: func(t *testing.T, c *MemoryCache) {
			require.NoError(t, c.Delete(t.Context(), "k"))
		}},
		{name: "clear", mutate: func(_ *testing.T, c *MemoryCache) {
			c.Clear()
		}},
		{name: "uncontended", want: &ToolsetSchema{ID: "refreshed"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() {
				close(release)
			})
			cache := NewMemoryCache(WithRefreshFunc(func(context.Context, string) (*ToolsetSchema, error) {
				close(started)
				<-release
				return &ToolsetSchema{ID: "refreshed"}, nil
			}))
			require.NoError(t, cache.Set(t.Context(), "k", &ToolsetSchema{ID: "old"}, 10*time.Hour))
			// Enter the refresh window without a wall-clock delay.
			cache.mu.Lock()
			cache.entries["k"].expiresAt = time.Now().Add(time.Hour)
			cache.mu.Unlock()
			cache.StartRefresh(t.Context())
			t.Cleanup(func() {
				unblock()
				cache.StopRefresh()
			})
			_, err := cache.Get(t.Context(), "k")
			require.NoError(t, err)
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("refresh did not start")
			}
			if tt.mutate != nil {
				tt.mutate(t, cache)
			}
			unblock()
			cache.StopRefresh()
			got, err := cache.Get(t.Context(), "k")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
