package contextlimits

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/stretchr/testify/assert"
)

func TestLookupWaiterCancellation(t *testing.T) {
	var cache Cache
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	completed := make(chan Result, 1)
	fallback := Result{Model: "alias"}
	expected := Result{Model: "resolved", Known: true, Limits: model.ContextLimits{ContextWindow: 1000}}
	load := func(ctx context.Context) (Result, bool) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return expected, true
		case <-ctx.Done():
			return fallback, false
		}
	}
	go func() {
		completed <- cache.Lookup(context.Background(), "alias", fallback, load)
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Equal(t, fallback, cache.Lookup(ctx, "alias", fallback, load))
	close(release)
	assert.Equal(t, expected, <-completed)
	assert.Equal(t, expected, cache.Lookup(context.Background(), "alias", fallback, load))
	assert.Equal(t, int32(1), calls.Load())
}

func TestLookupSuccessLifetime(t *testing.T) {
	now := time.Unix(100, 0)
	cache := Cache{now: func() time.Time { return now }}
	calls := 0
	expected := Result{Model: "resolved", Known: true}
	load := func(context.Context) (Result, bool) {
		calls++
		return expected, true
	}
	assert.Equal(t, expected, cache.Lookup(t.Context(), "alias", Result{}, load))
	now = now.Add(24 * time.Hour)
	assert.Equal(t, expected, cache.Lookup(t.Context(), "alias", Result{}, load))
	assert.Equal(t, 1, calls)
}

func TestLookupCallerCancellationNotCached(t *testing.T) {
	for _, expired := range []bool{false, true} {
		now := time.Unix(100, 0)
		cache := Cache{now: func() time.Time { return now }}
		calls := 0
		fallback := Result{Model: "alias", Known: true, Limits: model.ContextLimits{ContextWindow: 1000}}
		expected := Result{Model: "resolved", Known: true, Limits: model.ContextLimits{ContextWindow: 2000}}
		load := func(ctx context.Context) (Result, bool) {
			calls++
			if ctx.Err() != nil {
				return fallback, false
			}
			return expected, true
		}
		ctx, cancel := context.WithCancel(t.Context())
		if expired {
			cancel()
			ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		} else {
			cancel()
		}
		assert.Equal(t, fallback, cache.Lookup(ctx, "alias", fallback, load))
		cancel()
		assert.Equal(t, expected, cache.Lookup(t.Context(), "alias", fallback, load))
		assert.Equal(t, 2, calls)
	}
}

func TestLookupNegativeTTL(t *testing.T) {
	for _, known := range []bool{false, true} {
		now := time.Unix(100, 0)
		cache := Cache{now: func() time.Time { return now }}
		calls := 0
		fallback := Result{Model: "alias", Known: known}
		expected := Result{Model: "resolved", Known: true}
		load := func(context.Context) (Result, bool) {
			calls++
			if calls == 1 {
				return fallback, false
			}
			return expected, true
		}
		assert.Equal(t, fallback, cache.Lookup(t.Context(), "alias", fallback, load))
		now = now.Add(59 * time.Second)
		assert.Equal(t, fallback, cache.Lookup(t.Context(), "alias", fallback, load))
		assert.Equal(t, 1, calls)
		now = now.Add(time.Second)
		assert.Equal(t, expected, cache.Lookup(t.Context(), "alias", fallback, load))
		now = now.Add(24 * time.Hour)
		assert.Equal(t, expected, cache.Lookup(t.Context(), "alias", fallback, load))
		assert.Equal(t, 2, calls)
	}
}
