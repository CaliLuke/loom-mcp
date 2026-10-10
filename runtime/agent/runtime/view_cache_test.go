package runtime

import (
	"context"
	"sync"
	"testing"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blockedViewStore struct {
	runstore.Store
	once             sync.Once
	started, release chan struct{}
}

func TestViewCacheEvictionDuringLoad(t *testing.T) {
	store := storeinmem.New()
	base := buildGrowthIndex(t, store, 3)
	blocked := &blockedViewStore{Store: store, started: make(chan struct{}), release: make(chan struct{})}
	rt := New(WithRunStore(blocked), WithViewCacheRuns(1))
	var release sync.Once
	unblock := func() {
		release.Do(func() {
			close(blocked.release)
		})
	}
	t.Cleanup(unblock)
	type result struct {
		view *loadedView
		err  error
	}
	done := make(chan result, 1)
	go func() {
		view, err := rt.loadView(t.Context(), "root", "A", base.view())
		done <- result{view: view, err: err}
	}()
	select {
	case <-blocked.started:
	case <-t.Context().Done():
		t.Fatal("load did not reach the store")
	}
	_, err := rt.loadView(t.Context(), "root", "B", index.ViewRef{})
	require.NoError(t, err)
	require.NotContains(t, rt.viewCaches, "root\x00A", "an active load does not prevent eviction")
	unblock()
	loaded := <-done
	require.NoError(t, loaded.err)
	assert.NotContains(t, rt.viewCaches, "root\x00A", "finishing an evicted load does not reinsert it")
	cold, err := rt.loadView(t.Context(), "root", "A", index.ViewRef{IndexHead: loaded.view.Head})
	require.NoError(t, err)
	assert.Equal(t, loaded.view, cold, "eviction leaves the active cache pointer intact")
}

func TestViewCacheLRU(t *testing.T) {
	for _, tc := range []struct {
		name    string
		loads   []string
		evicted string
		kept    []string
	}{
		{name: "oldest run", loads: []string{"A", "B", "C"}, evicted: "A", kept: []string{"B", "C"}},
		{name: "recent use refreshes run", loads: []string{"A", "B", "A", "C"}, evicted: "B", kept: []string{"A", "C"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New(WithViewCacheRuns(2))
			for _, runID := range tc.loads {
				_, err := rt.loadView(t.Context(), "root", runID, index.ViewRef{})
				require.NoError(t, err)
				assert.LessOrEqual(t, len(rt.viewCaches), 2)
			}
			assert.NotContains(t, rt.viewCaches, "root\x00"+tc.evicted)
			for _, runID := range tc.kept {
				assert.Contains(t, rt.viewCaches, "root\x00"+runID)
			}
			assert.Equal(t, 2, rt.viewLRU.Len())
		})
	}
}

func TestEvictedViewReconstructsNextStep(t *testing.T) {
	for _, evict := range []bool{false, true} {
		name := "warm"
		if evict {
			name = "evicted"
		}
		t.Run(name, func(t *testing.T) {
			store := &measuredViewStore{Store: storeinmem.New()}
			base := buildGrowthIndex(t, store, 3)
			rt := New(WithRunStore(store), WithViewCacheRuns(2))
			baseline := New(WithRunStore(store))
			previous := index.ViewRef{IndexHead: base.IndexHead}
			_, err := baseline.loadView(t.Context(), "root", "A", previous)
			require.NoError(t, err)
			first, err := rt.loadView(t.Context(), "root", "A", previous)
			require.NoError(t, err)
			if evict {
				for _, runID := range []string{"B", "C"} {
					_, err := rt.loadView(t.Context(), "root", runID, previous)
					require.NoError(t, err)
				}
				require.NotContains(t, rt.viewCaches, "root\x00A")
			}
			expected, err := baseline.loadView(t.Context(), "root", "A", base.view())
			require.NoError(t, err)
			store.reads.Store(0)
			actual, err := rt.loadView(t.Context(), "root", "A", base.view())
			require.NoError(t, err)
			assert.Equal(t, expected, actual, "messages, outputs, typed answers and committed head survive eviction")
			assert.Equal(t, canonicalIndexMessages(t, expected.Messages), canonicalIndexMessages(t, actual.Messages))
			assert.Len(t, first.Messages, 4, "previously returned view remains intact")
			assert.Len(t, first.Outputs, 2)
			assert.Len(t, first.Typed, 2)
			reads := int64(5)
			if evict {
				reads = 15
			}
			assert.Equal(t, reads, store.reads.Load(), "cold reconstruction reads all three steps; warm reads only the new step")
		})
	}
}

func TestWithViewCacheRuns(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    int
	}{
		{name: "one", n: 1},
		{name: "two", n: 2},
		{name: "default-sized", n: 256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.n, New(WithViewCacheRuns(tc.n)).viewCacheRuns)
		})
	}
	assert.Equal(t, 256, New().viewCacheRuns)
	for _, tc := range []struct {
		name string
		n    int
	}{
		{name: "zero", n: 0},
		{name: "negative", n: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.PanicsWithValue(t, "runtime: view cache runs must be greater than zero", func() {
				WithViewCacheRuns(tc.n)
			})
		})
	}
	assert.PanicsWithValue(t, "runtime: view cache runs must be greater than zero", func() {
		newFromOptions(Options{ViewCacheRuns: -1})
	})
}

func (s *blockedViewStore) Get(ctx context.Context, root string, hash runstore.Hash) ([]byte, error) {
	block := false
	s.once.Do(func() {
		block = true
	})
	if block {
		close(s.started)
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.Store.Get(ctx, root, hash)
}
