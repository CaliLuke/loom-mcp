package registry

import (
	"context"
	"fmt"

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
	merged, errs := m.collectRegistrySearchResults(ctx, entries, query, func(ctx context.Context, name string, entry *registryEntry) ([]*SearchResult, error) {
		results, err := entry.client.Search(ctx, query)
		if err != nil {
			return nil, err
		}
		return tagSearchResults(name, results), nil
	})
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

type registrySearchFunc func(context.Context, string, *registryEntry) ([]*SearchResult, error)

func (m *Manager) collectRegistrySearchResults(
	ctx context.Context,
	entries map[string]*registryEntry,
	query string,
	search registrySearchFunc,
) ([]*SearchResult, []error) {
	type searchResult struct {
		registry string
		results  []*SearchResult
		err      error
	}

	resultCh := make(chan searchResult, len(entries))
	for name, entry := range entries {
		go func(name string, entry *registryEntry) {
			results, err := search(ctx, name, entry)
			if err != nil {
				m.obs.LogSearchFailure(ctx, name, query, err)
				resultCh <- searchResult{registry: name, err: err}
				return
			}
			resultCh <- searchResult{registry: name, results: results}
		}(name, entry)
	}

	var (
		merged []*SearchResult
		errs   []error
	)
	for range entries {
		res := <-resultCh
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
