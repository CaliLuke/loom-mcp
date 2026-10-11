// Package contextlimits caches activity-side provider model lookups.
package contextlimits

import (
	"context"
	"sync"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
)

// Result retains both a provider's limits and its resolved model ID.
type Result struct {
	// Limits is the cached token-limit snapshot.
	Limits model.ContextLimits
	// InputLimit is the raw input-only allowance returned by a Models API.
	// Zero means Limits already describes a shared window, such as table fallback.
	InputLimit int
	// Model is the resolved provider model ID.
	Model string
	// Known reports whether the context window is known.
	Known bool
}

// Cache coalesces model lookups and retains successful metadata for the client
// lifetime. Other failures retain fallback for negativeTTL; caller cancellations
// are never cached.
// Its zero value is ready for use; do not copy after first use.
type Cache struct {
	entries sync.Map
	// now is configured before first use; nil selects time.Now.
	now func() time.Time
}

type entry struct {
	done    chan struct{}
	result  Result
	expires time.Time
}

const (
	lookupTimeout = 2 * time.Second
	negativeTTL   = 60 * time.Second
)

// Lookup coalesces load per key with a bounded caller-derived deadline. load
// reports success separately from whether fallback limits are known. Waiters
// can cancel independently and receive fallback while another lookup is pending.
func (c *Cache) Lookup(ctx context.Context, key string, fallback Result, load func(context.Context) (Result, bool)) Result {
	for {
		candidate := &entry{done: make(chan struct{})}
		value, loaded := c.entries.LoadOrStore(key, candidate)
		cached := value.(*entry)
		if !loaded {
			lookupCtx, cancel := context.WithTimeout(ctx, lookupTimeout)
			result, success := load(lookupCtx)
			cancel()
			cached.result = result
			if !success {
				if ctx.Err() != nil {
					c.entries.CompareAndDelete(key, cached)
				} else {
					cached.expires = c.timeNow().Add(negativeTTL)
				}
			}
			close(cached.done)
			return result
		}
		select {
		case <-cached.done:
			if !cached.expires.IsZero() && !c.timeNow().Before(cached.expires) {
				c.entries.CompareAndDelete(key, cached)
				continue
			}
			return cached.result
		case <-ctx.Done():
			return fallback
		}
	}
}

func (c *Cache) timeNow() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
