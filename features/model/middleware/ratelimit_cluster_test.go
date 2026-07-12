package middleware

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom/pulse/rmap"
)

type fakeClusterMap struct {
	mu           sync.Mutex
	values       map[string]string
	ch           chan rmap.EventKind
	updated      chan struct{}
	unsubscribed chan struct{}
	unsubOnce    sync.Once
}

func newFakeClusterMap() *fakeClusterMap {
	return &fakeClusterMap{
		values:       make(map[string]string),
		ch:           make(chan rmap.EventKind, 1),
		updated:      make(chan struct{}, 1),
		unsubscribed: make(chan struct{}),
	}
}

func (m *fakeClusterMap) Get(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.values[key]
	return v, ok
}

func (m *fakeClusterMap) SetIfNotExists(_ context.Context, key, value string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.values[key]; ok {
		return false, nil
	}
	m.values[key] = value
	select {
	case m.ch <- rmap.EventChange:
	default:
	}
	return true, nil
}

func (m *fakeClusterMap) TestAndSet(_ context.Context, key, test, value string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.values[key]
	if !ok || cur != test {
		return cur, nil
	}
	m.values[key] = value
	select {
	case m.updated <- struct{}{}:
	default:
	}
	select {
	case m.ch <- rmap.EventChange:
	default:
	}
	return cur, nil
}

func (m *fakeClusterMap) Subscribe() <-chan rmap.EventKind {
	return m.ch
}

func (m *fakeClusterMap) Unsubscribe(ch <-chan rmap.EventKind) {
	if ch != m.ch {
		return
	}
	m.unsubOnce.Do(func() {
		close(m.unsubscribed)
	})
}

func TestClusterLimiter_BackoffUpdatesSharedMap(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := newFakeClusterMap()
	const key = "model"

	// Seed map with initial value.
	m.values[key] = strconv.Itoa(80000)

	lim := newClusterAdaptiveRateLimiter(ctx, m, key, 80000, 80000)

	client := &fakeClient{
		completeErr: model.ErrRateLimited,
	}
	wrapped := lim.Middleware()(client)

	req := model.Request{
		Messages: []*model.Message{
			{
				Role: model.ConversationRoleUser,
				Parts: []model.Part{
					model.TextPart{Text: "hello"},
				},
			},
		},
		MaxTokens: 10,
	}

	_, _ = wrapped.Complete(context.Background(), &req)

	select {
	case <-m.updated:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for shared TPM update")
	}

	v, ok := m.Get(key)
	if !ok {
		t.Fatal("expected key to exist in cluster map")
	}
	cur, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("invalid value in cluster map: %v", err)
	}
	if cur >= 80000 {
		t.Fatalf("expected shared TPM to decrease, got %d", cur)
	}
}

func TestWatchSharedTPMStopsWhenContextCanceled(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	m := newFakeClusterMap()
	const key = "model"
	m.values[key] = strconv.Itoa(80000)

	lim := newAdaptiveRateLimiter(80000, 80000)
	done := watchSharedTPM(ctx, m, key, lim)

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected shared TPM watcher to stop after context cancellation")
	}

	select {
	case <-m.unsubscribed:
	default:
		t.Fatal("expected shared TPM watcher to unsubscribe")
	}
}
