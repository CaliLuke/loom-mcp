package registry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

const defaultRegistryCacheTTL = time.Hour

// DiscoverToolset retrieves a toolset schema from the specified registry.
// It first checks the cache, then falls back to the registry client.
func (m *Manager) DiscoverToolset(ctx context.Context, registry, toolset string) (*ToolsetSchema, error) {
	var (
		outcome OperationOutcome
		opErr   error
	)
	obs := m.observeOperation(
		ctx,
		OperationEvent{
			Operation: OpDiscoverToolset,
			Registry:  registry,
			Toolset:   toolset,
			CacheKey:  cacheKey(registry, toolset),
		},
		nil,
		&opErr,
		&outcome,
		attribute.String("registry", registry),
		attribute.String("toolset", toolset),
	)
	defer obs.finish()

	entry, err := m.lookupRegistry(registry)
	if err != nil {
		outcome = OutcomeError
		opErr = err
		return nil, opErr
	}

	schema, err := m.lookupCachedToolset(ctx, registry, toolset, obs.span)
	if err == nil && schema != nil {
		outcome = OutcomeCacheHit
		return schema, nil
	}

	schema, err = m.fetchToolset(ctx, entry, toolset)
	if err != nil {
		fallback, fallbackErr := m.lookupCachedToolset(ctx, registry, toolset, obs.span)
		if fallbackErr == nil && fallback != nil {
			obs.span.AddEvent("fallback_to_cache", "cache_key", cacheKey(registry, toolset))
			outcome = OutcomeFallback
			return fallback, nil
		}
		outcome = OutcomeError
		opErr = fmt.Errorf("fetching toolset %q from registry %q: %w", toolset, registry, err)
		return nil, opErr
	}

	schema.Origin = registry
	m.cacheToolset(ctx, registry, toolset, schema, entry.cacheTTL, "failed to cache toolset")
	outcome = OutcomeSuccess
	return schema, nil
}

func (m *Manager) lookupRegistry(name string) (*registryEntry, error) {
	m.mu.RLock()
	entry, ok := m.registries[name]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("registry %q not found", name)
	}
	return entry, nil
}

func (m *Manager) lookupCachedToolset(
	ctx context.Context,
	registry string,
	toolset string,
	span interface{ AddEvent(string, ...any) },
) (*ToolsetSchema, error) {
	key := cacheKey(registry, toolset)
	schema, err := m.cache.Get(ctx, key)
	if err != nil || schema == nil {
		span.AddEvent("cache_miss", "cache_key", key)
		return nil, err
	}
	span.AddEvent("cache_hit", "cache_key", key)
	return schema, nil
}

func (m *Manager) fetchToolset(ctx context.Context, entry *registryEntry, toolset string) (*ToolsetSchema, error) {
	return entry.client.GetToolset(ctx, toolset)
}

func (m *Manager) cacheToolset(
	ctx context.Context,
	registry string,
	toolset string,
	schema *ToolsetSchema,
	ttl time.Duration,
	message string,
) {
	if ttl == 0 {
		ttl = defaultRegistryCacheTTL
	}
	if err := m.cache.Set(ctx, cacheKey(registry, toolset), schema, ttl); err != nil {
		m.logger.Warn(ctx, message, "registry", registry, "toolset", toolset, "error", err)
	}
}
