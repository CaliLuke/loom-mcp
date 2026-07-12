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
		mu             sync.Mutex
		calls          int
		firstCanceled  = make(chan struct{})
		releaseFirst   = make(chan struct{})
		callStarted    = make(chan int, 2)
		cancelObserved sync.Once
	)
	countCalls := func() int {
		mu.Lock()
		defer mu.Unlock()

		return calls
	}
	client := &mockRegistryClientWithFuncs{
		listToolsetsFunc: func(ctx context.Context) ([]*ToolsetInfo, error) {
			mu.Lock()
			calls++
			call := calls
			mu.Unlock()
			callStarted <- call
			if call == 1 {
				<-ctx.Done()
				cancelObserved.Do(func() {
					close(firstCanceled)
				})
				<-releaseFirst
			}
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
	require.Equal(t, 1, <-callStarted)

	stopDone := make(chan struct{})
	go func() {
		m.StopSync()
		close(stopDone)
	}()
	<-firstCanceled
	select {
	case <-stopDone:
		t.Fatal("StopSync returned before the in-flight sync exited")
	default:
	}
	close(releaseFirst)
	<-stopDone

	stopped := countCalls()
	assert.Equal(t, 1, stopped)

	require.NoError(t, m.StartSync(context.Background()))
	require.Equal(t, stopped+1, <-callStarted)

	m.StopSync()
}
