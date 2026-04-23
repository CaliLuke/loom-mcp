package registry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// StartSync starts the background sync loop for all registries.
// Each registry is synced at its configured SyncInterval.
func (m *Manager) StartSync(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.syncCancel != nil {
		return fmt.Errorf("sync loop already running")
	}

	m.syncCtx, m.syncCancel = context.WithCancel(ctx)
	for name, entry := range m.registries {
		if entry.syncInterval <= 0 {
			continue
		}
		m.syncWg.Add(1)
		go m.syncRegistry(name, entry)
	}

	m.obs.LogSyncLifecycle(ctx, "started")
	return nil
}

// StopSync stops the background sync loop.
func (m *Manager) StopSync() {
	m.mu.Lock()
	cancel := m.syncCancel
	m.syncCancel = nil
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	m.syncWg.Wait()

	m.obs.LogSyncLifecycle(context.Background(), "stopped")
}

// syncRegistry runs the sync loop for a single registry.
func (m *Manager) syncRegistry(name string, entry *registryEntry) {
	defer m.syncWg.Done()

	ticker := time.NewTicker(entry.syncInterval)
	defer ticker.Stop()

	m.doSync(name, entry)
	for {
		select {
		case <-m.syncCtx.Done():
			return
		case <-ticker.C:
			m.doSync(name, entry)
		}
	}
}

// doSync performs a single sync operation for a registry.
func (m *Manager) doSync(name string, entry *registryEntry) {
	var (
		outcome     OperationOutcome
		opErr       error
		resultCount int
	)
	ctx := m.syncCtx
	if ctx == nil {
		ctx = context.Background()
	}
	obs := m.observeOperation(
		ctx,
		OperationEvent{
			Operation: OpSync,
			Registry:  name,
		},
		&resultCount,
		&opErr,
		&outcome,
		attribute.String("registry", name),
	)
	defer obs.finish()

	toolsets, err := entry.client.ListToolsets(obs.ctx)
	if err != nil {
		outcome = OutcomeError
		opErr = err
		return
	}

	filtered := m.filterFederated(toolsets, entry.federation)
	if entry.federation != nil {
		obs.span.AddEvent(
			"federation_filter_applied",
			"original_count", len(toolsets),
			"filtered_count", len(filtered),
		)
	}
	resultCount = len(filtered)
	m.cacheSyncedToolsets(obs.ctx, name, entry, filtered)
	outcome = OutcomeSuccess
}

func (m *Manager) cacheSyncedToolsets(ctx context.Context, registry string, entry *registryEntry, toolsets []*ToolsetInfo) {
	for _, toolset := range toolsets {
		toolset.Origin = registry
		schema, err := entry.client.GetToolset(ctx, toolset.Name)
		if err != nil {
			m.obs.LogSyncFetchFailure(ctx, registry, toolset.Name, err)
			continue
		}
		schema.Origin = registry
		m.cacheToolset(ctx, registry, toolset.Name, schema, entry.cacheTTL, "failed to cache toolset during sync")
	}
}

// filterFederated applies Include/Exclude patterns to filter toolsets.
// Include patterns whitelist namespaces; Exclude patterns blacklist them.
// If Include is empty, all namespaces are included by default.
func (m *Manager) filterFederated(toolsets []*ToolsetInfo, cfg *FederationConfig) []*ToolsetInfo {
	if cfg == nil {
		return toolsets
	}

	filtered := make([]*ToolsetInfo, 0, len(toolsets))
	for _, toolset := range toolsets {
		if m.shouldInclude(toolset.Name, cfg) {
			filtered = append(filtered, toolset)
		}
	}
	return filtered
}

// shouldInclude determines if a toolset should be included based on federation config.
func (m *Manager) shouldInclude(name string, cfg *FederationConfig) bool {
	for _, pattern := range cfg.Exclude {
		if matchGlob(pattern, name) {
			return false
		}
	}
	if len(cfg.Include) == 0 {
		return true
	}
	for _, pattern := range cfg.Include {
		if matchGlob(pattern, name) {
			return true
		}
	}
	return false
}
