package registry

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/attribute"
)

// Search performs a search across all registries and merges results.
// Results are tagged with their origin registry.
func (m *Manager) Search(ctx context.Context, query string) ([]*SearchResult, error) {
	var (
		outcome     OperationOutcome
		opErr       error
		resultCount int
	)
	obs := m.observeOperation(
		ctx,
		OperationEvent{
			Operation: OpSearch,
			Query:     query,
		},
		&resultCount,
		&opErr,
		&outcome,
		attribute.String("query", query),
	)
	defer obs.finish()

	entries := m.snapshotRegistries()
	if len(entries) == 0 {
		outcome = OutcomeSuccess
		return nil, nil
	}

	obs.span.AddEvent("searching_registries", "registry_count", len(entries))
	merged, errs := m.searchRegistries(ctx, entries, query)
	resultCount = len(merged)
	if len(errs) > 0 && len(merged) > 0 {
		obs.span.AddEvent("partial_failure", "error_count", len(errs), "result_count", len(merged))
	}
	if len(errs) == len(entries) && len(errs) > 0 {
		outcome = OutcomeError
		opErr = fmt.Errorf("all registries failed: %v", errs)
		return nil, opErr
	}

	outcome = OutcomeSuccess
	return merged, nil
}

func (m *Manager) snapshotRegistries() map[string]*registryEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entries := make(map[string]*registryEntry, len(m.registries))
	for name, entry := range m.registries {
		entries[name] = entry
	}
	return entries
}

func (m *Manager) searchRegistries(
	ctx context.Context,
	entries map[string]*registryEntry,
	query string,
) ([]*SearchResult, []error) {
	type searchResult struct {
		registry string
		results  []*SearchResult
		err      error
	}

	resultCh := make(chan searchResult, len(entries))
	var wg sync.WaitGroup
	for name, entry := range entries {
		wg.Add(1)
		go func(name string, entry *registryEntry) {
			defer wg.Done()
			results, err := entry.client.Search(ctx, query)
			if err != nil {
				m.logger.Warn(ctx, "search failed for registry", "registry", name, "query", query, "error", err)
				resultCh <- searchResult{registry: name, err: err}
				return
			}
			resultCh <- searchResult{registry: name, results: tagSearchResults(name, results)}
		}(name, entry)
	}
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var (
		merged []*SearchResult
		errs   []error
	)
	for res := range resultCh {
		if res.err != nil {
			errs = append(errs, fmt.Errorf("registry %q: %w", res.registry, res.err))
			continue
		}
		merged = append(merged, res.results...)
	}
	return merged, errs
}

func tagSearchResults(registry string, results []*SearchResult) []*SearchResult {
	for _, result := range results {
		if result.Origin == "" {
			result.Origin = registry
		}
	}
	return results
}
