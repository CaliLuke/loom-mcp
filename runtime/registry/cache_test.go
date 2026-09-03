package registry

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const refreshedSchemaVersion = "2.0"

// TestMemoryCacheGetSetDelete tests basic cache operations.
// **Validates: Requirements 8.1**
//
//nolint:cyclop // The end-to-end cache CRUD assertions are clearer in one test.
func TestMemoryCacheGetSetDelete(t *testing.T) {
	ctx := context.Background()
	cache := NewMemoryCache()

	// Test Set and Get
	schema := &ToolsetSchema{
		ID:          "test-id",
		Name:        "test-toolset",
		Description: "A test toolset",
		Version:     "1.0.0",
		Tools: []*ToolSchema{
			{Name: "tool1", Description: "Tool 1", PayloadSchema: []byte(`{"type":"object"}`)},
		},
	}

	err := cache.Set(ctx, "key1", schema, time.Hour)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	got, err := cache.Get(ctx, "key1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil for existing key")
	}
	if got.ID != schema.ID {
		t.Errorf("Get returned wrong ID: got %q, want %q", got.ID, schema.ID)
	}
	if got.Name != schema.Name {
		t.Errorf("Get returned wrong Name: got %q, want %q", got.Name, schema.Name)
	}

	// Test Get for non-existent key
	got, err = cache.Get(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Get for nonexistent key failed: %v", err)
	}
	if got != nil {
		t.Error("Get returned non-nil for nonexistent key")
	}

	// Test Delete
	err = cache.Delete(ctx, "key1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	got, err = cache.Get(ctx, "key1")
	if err != nil {
		t.Fatalf("Get after Delete failed: %v", err)
	}
	if got != nil {
		t.Error("Get returned non-nil after Delete")
	}
}

// TestMemoryCacheTTLExpiration tests that entries expire after TTL.
// **Validates: Requirements 8.1, 8.3**
func TestMemoryCacheTTLExpiration(t *testing.T) {
	ctx := context.Background()
	cache := NewMemoryCache()

	schema := &ToolsetSchema{
		ID:   "expiring-id",
		Name: "expiring-toolset",
	}

	// Set with very short TTL
	err := cache.Set(ctx, "expiring-key", schema, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Should be available immediately
	got, err := cache.Get(ctx, "expiring-key")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil before TTL expiration")
	}

	waitForCondition(t, func() bool {
		got, err = cache.Get(ctx, "expiring-key")
		return err == nil && got == nil
	}, "expected cache entry to expire")
	if got != nil {
		t.Error("Get returned non-nil after TTL expiration")
	}
}

func TestMemoryCacheExpiredDeleteDoesNotRemoveRefreshedEntry(t *testing.T) {
	cache := NewMemoryCache()
	key := "refreshing-key"
	expired := &cacheEntry{
		schema:    &ToolsetSchema{ID: "old", Name: "old"},
		expiresAt: time.Now().Add(-time.Second),
		ttl:       time.Second,
	}
	fresh := &cacheEntry{
		schema:    &ToolsetSchema{ID: "new", Name: "new"},
		expiresAt: time.Now().Add(time.Hour),
		ttl:       time.Hour,
	}
	cache.entries[key] = expired
	cache.entries[key] = fresh

	cache.deleteExpiredEntry(key, expired)

	got, err := cache.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got == nil {
		t.Fatal("fresh entry was deleted")
	}
	if got.ID != "new" {
		t.Errorf("Get returned %q, want %q", got.ID, "new")
	}
}

// TestMemoryCacheClear tests the Clear method.
// **Validates: Requirements 8.1**
func TestMemoryCacheClear(t *testing.T) {
	ctx := context.Background()
	cache := NewMemoryCache()

	// Add multiple entries
	for i := range 5 {
		schema := &ToolsetSchema{
			ID:   string(rune('a' + i)),
			Name: string(rune('a' + i)),
		}
		if err := cache.Set(ctx, schema.ID, schema, time.Hour); err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	}

	if cache.Len() != 5 {
		t.Errorf("Len before Clear: got %d, want 5", cache.Len())
	}

	cache.Clear()

	if cache.Len() != 0 {
		t.Errorf("Len after Clear: got %d, want 0", cache.Len())
	}
}

// TestMemoryCacheLen tests the Len method.
// **Validates: Requirements 8.1**
func TestMemoryCacheLen(t *testing.T) {
	ctx := context.Background()
	cache := NewMemoryCache()

	if cache.Len() != 0 {
		t.Errorf("Len of empty cache: got %d, want 0", cache.Len())
	}

	schema := &ToolsetSchema{ID: "test", Name: "test"}
	if err := cache.Set(ctx, "key1", schema, time.Hour); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	if cache.Len() != 1 {
		t.Errorf("Len after one Set: got %d, want 1", cache.Len())
	}

	if err := cache.Set(ctx, "key2", schema, time.Hour); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	if cache.Len() != 2 {
		t.Errorf("Len after two Sets: got %d, want 2", cache.Len())
	}
}

// TestMemoryCacheOverwrite tests that Set overwrites existing entries.
// **Validates: Requirements 8.1**
func TestMemoryCacheOverwrite(t *testing.T) {
	ctx := context.Background()
	cache := NewMemoryCache()

	schema1 := &ToolsetSchema{ID: "id1", Name: "name1", Version: "1.0"}
	schema2 := &ToolsetSchema{ID: "id2", Name: "name2", Version: "2.0"}

	if err := cache.Set(ctx, "key", schema1, time.Hour); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	got, _ := cache.Get(ctx, "key")
	if got.Version != "1.0" {
		t.Errorf("Version before overwrite: got %q, want %q", got.Version, "1.0")
	}

	if err := cache.Set(ctx, "key", schema2, time.Hour); err != nil {
		t.Fatalf("Set (overwrite) failed: %v", err)
	}

	got, _ = cache.Get(ctx, "key")
	if got.Version != "2.0" {
		t.Errorf("Version after overwrite: got %q, want %q", got.Version, "2.0")
	}

	if cache.Len() != 1 {
		t.Errorf("Len after overwrite: got %d, want 1", cache.Len())
	}
}

// TestMemoryCacheConcurrency tests concurrent access to the cache.
// **Validates: Requirements 8.1**
func TestMemoryCacheConcurrency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cache := NewMemoryCache()

	var wg sync.WaitGroup
	numGoroutines := 10
	numOperations := 100

	// Concurrent writes
	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range numOperations {
				schema := &ToolsetSchema{
					ID:   "concurrent",
					Name: "concurrent",
				}
				key := string(rune('a' + (id+j)%26))
				_ = cache.Set(ctx, key, schema, time.Hour)
			}
		}(i)
	}

	// Concurrent reads
	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range numOperations {
				key := string(rune('a' + j%26))
				_, _ = cache.Get(ctx, key)
			}
		}()
	}

	wg.Wait()
	// Test passes if no race conditions or panics occur
}

// TestMemoryCacheDeleteNonExistent tests deleting a non-existent key.
// **Validates: Requirements 8.1**
func TestMemoryCacheDeleteNonExistent(t *testing.T) {
	ctx := context.Background()
	cache := NewMemoryCache()

	// Delete non-existent key should not error
	err := cache.Delete(ctx, "nonexistent")
	if err != nil {
		t.Errorf("Delete of nonexistent key returned error: %v", err)
	}
}

// TestMemoryCacheBackgroundRefresh tests the background refresh functionality.
// **Validates: Requirements 8.3**
func TestMemoryCacheBackgroundRefresh(t *testing.T) {
	ctx := context.Background()

	refreshCalled := make(chan string, 10)
	refreshFunc := func(_ context.Context, key string) (*ToolsetSchema, error) {
		refreshCalled <- key
		return &ToolsetSchema{
			ID:      "refreshed-" + key,
			Name:    "refreshed",
			Version: refreshedSchemaVersion,
		}, nil
	}

	cache := NewMemoryCache(
		WithRefreshFunc(refreshFunc),
		WithRefreshCooldown(10*time.Millisecond),
	)

	// Start refresh loop
	cache.StartRefresh(ctx)
	defer cache.StopRefresh()

	// Put the entry inside its refresh window without relying on a narrow
	// wall-clock interval that can expire under the race detector.
	schema := &ToolsetSchema{ID: "original", Name: "original", Version: "1.0"}
	if err := cache.Set(ctx, "refresh-key", schema, time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	cache.mu.Lock()
	cache.entries["refresh-key"].expiresAt = time.Now().Add(10 * time.Second)
	cache.mu.Unlock()

	// Trigger refresh by accessing the entry
	_, _ = cache.Get(ctx, "refresh-key")

	// Wait for refresh to complete
	select {
	case key := <-refreshCalled:
		if key != "refresh-key" {
			t.Errorf("Refresh called with wrong key: got %q, want %q", key, "refresh-key")
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Refresh was not triggered within timeout")
	}

	var got *ToolsetSchema
	waitForCondition(t, func() bool {
		got, _ = cache.Get(ctx, "refresh-key")
		return got != nil && got.Version == "2.0"
	}, "expected refreshed cache entry")
	if got == nil {
		t.Fatal("Get returned nil after refresh")
	}
	if got.Version != refreshedSchemaVersion {
		t.Errorf("Version after refresh: got %q, want %q", got.Version, refreshedSchemaVersion)
	}
}

// TestMemoryCacheRefreshCooldown tests that refresh respects cooldown period.
// **Validates: Requirements 8.3**
func TestMemoryCacheRefreshCooldown(t *testing.T) {
	ctx := context.Background()

	refreshCount := 0
	var mu sync.Mutex
	refreshFunc := func(_ context.Context, _ string) (*ToolsetSchema, error) {
		mu.Lock()
		refreshCount++
		mu.Unlock()
		return &ToolsetSchema{ID: "refreshed", Name: "refreshed"}, nil
	}

	cache := NewMemoryCache(
		WithRefreshFunc(refreshFunc),
		WithRefreshCooldown(200*time.Millisecond),
	)

	cache.StartRefresh(ctx)
	defer cache.StopRefresh()

	// Put the entry deterministically inside its refresh window. Sleeping until
	// the final 20% of a 50 ms TTL made this test race expiration under load.
	schema := &ToolsetSchema{ID: "original", Name: "original"}
	if err := cache.Set(ctx, "cooldown-key", schema, time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	cache.mu.Lock()
	cache.entries["cooldown-key"].expiresAt = time.Now().Add(time.Second)
	cache.mu.Unlock()

	// Multiple Gets should only trigger one refresh due to cooldown
	for range 5 {
		_, _ = cache.Get(ctx, "cooldown-key")
	}
	waitForCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return refreshCount >= 1
	}, "expected refresh to be triggered")

	mu.Lock()
	count := refreshCount
	mu.Unlock()

	// Should have at most 1 refresh due to cooldown
	if count > 1 {
		t.Errorf("Refresh called %d times, expected at most 1 due to cooldown", count)
	}
}

// TestMemoryCacheRefreshNotStarted tests that refresh doesn't trigger when not started.
// **Validates: Requirements 8.3**
func TestMemoryCacheRefreshNotStarted(t *testing.T) {
	ctx := context.Background()

	refreshCalled := false
	refreshFunc := func(_ context.Context, _ string) (*ToolsetSchema, error) {
		refreshCalled = true
		return &ToolsetSchema{ID: "refreshed", Name: "refreshed"}, nil
	}

	cache := NewMemoryCache(
		WithRefreshFunc(refreshFunc),
	)
	// Note: NOT calling StartRefresh

	schema := &ToolsetSchema{ID: "original", Name: "original"}
	if err := cache.Set(ctx, "no-refresh-key", schema, 50*time.Millisecond); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	waitForCondition(t, func() bool {
		cache.mu.RLock()
		entry, ok := cache.entries["no-refresh-key"]
		cache.mu.RUnlock()
		if !ok {
			return false
		}
		return time.Now().After(entry.expiresAt.Add(-entry.ttl / 5))
	}, "expected no-refresh-key to enter refresh window")

	// Get should not trigger refresh since loop is not started
	_, _ = cache.Get(ctx, "no-refresh-key")

	if refreshCalled {
		t.Error("Refresh was called even though StartRefresh was not called")
	}
}

func TestMemoryCacheStartRefreshIdempotent(t *testing.T) {
	ctx := context.Background()

	var refreshCount atomic.Int32
	refreshFunc := func(_ context.Context, _ string) (*ToolsetSchema, error) {
		refreshCount.Add(1)
		return &ToolsetSchema{ID: "refreshed", Name: "refreshed"}, nil
	}

	cache := NewMemoryCache(
		WithRefreshFunc(refreshFunc),
		WithRefreshCooldown(time.Hour),
	)
	cache.StartRefresh(ctx)
	cache.StartRefresh(ctx)
	cache.StartRefresh(ctx)
	defer cache.StopRefresh()

	schema := &ToolsetSchema{ID: "original", Name: "original"}
	if err := cache.Set(ctx, "idempotent-key", schema, 200*time.Millisecond); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	waitForCondition(t, func() bool {
		cache.mu.RLock()
		entry, ok := cache.entries["idempotent-key"]
		cache.mu.RUnlock()
		if !ok {
			return false
		}
		return time.Now().After(entry.expiresAt.Add(-entry.ttl / 5))
	}, "expected idempotent-key to enter refresh window")

	for range 20 {
		_, _ = cache.Get(ctx, "idempotent-key")
	}

	waitForCondition(t, func() bool {
		return refreshCount.Load() > 0
	}, "expected refresh to run")

	time.Sleep(50 * time.Millisecond)
	if got := refreshCount.Load(); got != 1 {
		t.Errorf("Refresh called %d times, want 1", got)
	}
}

func TestMemoryCacheRefreshLifecycleRaceFree(t *testing.T) {
	ctx := context.Background()

	refreshFunc := func(ctx context.Context, key string) (*ToolsetSchema, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return &ToolsetSchema{ID: "refreshed-" + key, Name: "refreshed"}, nil
		}
	}

	cache := NewMemoryCache(
		WithRefreshFunc(refreshFunc),
		WithRefreshCooldown(time.Nanosecond),
	)
	schema := &ToolsetSchema{ID: "original", Name: "original"}
	if err := cache.Set(ctx, "race-key", schema, time.Hour); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				cache.StartRefresh(ctx)
				cache.StopRefresh()
			}
		}()
	}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				cache.StartRefresh(ctx)
				cache.triggerRefresh("race-key")
				_, _ = cache.Get(ctx, "race-key")
			}
		}()
	}

	wg.Wait()
	cache.StopRefresh()
}

// TestNoopCacheImplementsInterface tests that noopCache implements Cache interface.
// **Validates: Requirements 8.1**
func TestNoopCacheImplementsInterface(t *testing.T) {
	var _ Cache = &noopCache{}

	ctx := context.Background()
	cache := &noopCache{}

	// Get always returns nil, nil
	got, err := cache.Get(ctx, "any-key")
	if err != nil {
		t.Errorf("noopCache.Get returned error: %v", err)
	}
	if got != nil {
		t.Error("noopCache.Get returned non-nil")
	}

	// Set always succeeds
	err = cache.Set(ctx, "any-key", &ToolsetSchema{}, time.Hour)
	if err != nil {
		t.Errorf("noopCache.Set returned error: %v", err)
	}

	// Delete always succeeds
	err = cache.Delete(ctx, "any-key")
	if err != nil {
		t.Errorf("noopCache.Delete returned error: %v", err)
	}
}
