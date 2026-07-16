package registry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSearchClientFansOutAcrossRegistriesConcurrently(t *testing.T) {
	t.Parallel()

	entered := make(chan string, 2)
	release := make(chan struct{})
	manager := NewManager()
	manager.AddRegistry("one", &blockingSearchClient{name: "one", entered: entered, release: release}, RegistryConfig{})
	manager.AddRegistry("two", &blockingSearchClient{name: "two", entered: entered, release: release}, RegistryConfig{})

	done := make(chan error, 1)
	go func() {
		_, err := NewSearchClient(manager).Search(context.Background(), "query", SearchOptions{})
		done <- err
	}()

	seen := make(map[string]bool, 2)
	for range 2 {
		select {
		case name := <-entered:
			seen[name] = true
		case <-time.After(time.Second):
			close(release)
			t.Fatal("search did not fan out to every registry concurrently")
		}
	}
	close(release)
	require.NoError(t, <-done)
	require.Equal(t, map[string]bool{"one": true, "two": true}, seen)
}

func TestManagerAndSearchClientSharePartialFailureContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		search func(context.Context, *Manager) ([]*SearchResult, error)
	}{
		{
			name: "manager",
			search: func(ctx context.Context, manager *Manager) ([]*SearchResult, error) {
				return manager.Search(ctx, "query")
			},
		},
		{
			name: "semantic client",
			search: func(ctx context.Context, manager *Manager) ([]*SearchResult, error) {
				return NewSearchClient(manager).Search(ctx, "query", SearchOptions{})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			manager := NewManager()
			manager.AddRegistry("available", &staticSearchClient{results: []*SearchResult{{ID: "tool", Name: "tool"}}}, RegistryConfig{})
			manager.AddRegistry("unavailable", &staticSearchClient{err: errors.New("offline")}, RegistryConfig{})

			results, err := tt.search(t.Context(), manager)
			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Equal(t, "available", results[0].Origin)

			allFailed := NewManager()
			allFailed.AddRegistry("unavailable", &staticSearchClient{err: errors.New("offline")}, RegistryConfig{})
			_, err = tt.search(t.Context(), allFailed)
			require.ErrorContains(t, err, "all registries failed")
		})
	}
}

type blockingSearchClient struct {
	once    sync.Once
	name    string
	entered chan<- string
	release <-chan struct{}
}

type staticSearchClient struct {
	results []*SearchResult
	err     error
}

func (c *staticSearchClient) ListToolsets(context.Context) ([]*ToolsetInfo, error) {
	return nil, nil
}

func (c *staticSearchClient) GetToolset(context.Context, string) (*ToolsetSchema, error) {
	return nil, nil
}

func (c *staticSearchClient) Search(context.Context, string) ([]*SearchResult, error) {
	return c.results, c.err
}

func (c *blockingSearchClient) ListToolsets(context.Context) ([]*ToolsetInfo, error) {
	return nil, nil
}

func (c *blockingSearchClient) GetToolset(context.Context, string) (*ToolsetSchema, error) {
	return nil, nil
}

func (c *blockingSearchClient) Search(ctx context.Context, _ string) ([]*SearchResult, error) {
	c.once.Do(func() {
		c.entered <- c.name
	})
	select {
	case <-c.release:
		return []*SearchResult{{ID: c.name, Name: c.name}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
