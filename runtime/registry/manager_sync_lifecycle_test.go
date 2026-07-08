package registry

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSyncLifecycleConcurrentStartStop hammers StartSync/StopSync from
// concurrent goroutines. Under -race this catches unsynchronized sync-context
// accesses, and the timeout catches the historical hang where StopSync waited
// forever on goroutines that had observed a newer generation's context.
func TestSyncLifecycleConcurrentStartStop(t *testing.T) {
	m := NewManager()
	client := &mockRegistryClientWithFuncs{
		toolsets: []*ToolsetInfo{{ID: "ts-1", Name: "stress-toolset"}},
	}
	m.AddRegistry("stress-registry", client, RegistryConfig{
		SyncInterval: time.Millisecond,
		CacheTTL:     time.Hour,
	})

	const (
		workers    = 4
		iterations = 50
	)

	var wg sync.WaitGroup
	for range workers {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range iterations {
				if err := m.StartSync(context.Background()); err != nil {
					assert.EqualError(t, err, "sync loop already running")
				}
			}
		}()
		go func() {
			defer wg.Done()
			for range iterations {
				m.StopSync()
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent StartSync/StopSync deadlocked")
	}

	m.StopSync()
}

// TestSyncLifecycleRestart verifies that StopSync fully terminates the old
// generation's sync goroutines and that a subsequent StartSync runs a fresh
// generation.
func TestSyncLifecycleRestart(t *testing.T) {
	var (
		mu    sync.Mutex
		calls int
	)
	countCalls := func() int {
		mu.Lock()
		defer mu.Unlock()

		return calls
	}
	client := &mockRegistryClientWithFuncs{
		listToolsetsFunc: func(_ context.Context) ([]*ToolsetInfo, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			return []*ToolsetInfo{{ID: "ts-1", Name: "restart-toolset"}}, nil
		},
		getToolsetFunc: func(_ context.Context, _ string) (*ToolsetSchema, error) {
			return &ToolsetSchema{ID: "ts-1", Name: "restart-toolset"}, nil
		},
	}

	m := NewManager(WithCache(NewMemoryCache()))
	m.AddRegistry("restart-registry", client, RegistryConfig{
		SyncInterval: 20 * time.Millisecond,
		CacheTTL:     time.Hour,
	})

	require.NoError(t, m.StartSync(context.Background()))
	waitForCondition(t, func() bool {
		return countCalls() >= 2
	}, "first sync generation did not run")

	m.StopSync()

	// StopSync waits on syncWg, so every goroutine of the old generation has
	// exited; the call counter must stay frozen across several intervals.
	stopped := countCalls()
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, stopped, countCalls(), "old sync goroutine kept running after StopSync")

	require.NoError(t, m.StartSync(context.Background()))
	waitForCondition(t, func() bool {
		return countCalls() > stopped
	}, "restarted sync generation did not run")

	m.StopSync()
}
