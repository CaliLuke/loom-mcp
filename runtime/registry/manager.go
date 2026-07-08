// Package registry provides runtime components for managing MCP registry
// connections, tool discovery, and catalog synchronization.
package registry

import (
	"context"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/telemetry"
)

type (
	// Manager coordinates multiple registry clients, providing unified
	// discovery, search, and caching across all configured registries.
	Manager struct {
		mu         sync.RWMutex
		registries map[string]*registryEntry
		cache      Cache
		logger     telemetry.Logger
		metrics    telemetry.Metrics
		tracer     telemetry.Tracer
		obs        *Observability

		// Sync loop control. syncLifecycleMu serializes StartSync/StopSync so
		// a restart cannot overlap a stop sequence (cancel + Wait); syncCancel
		// is additionally guarded by mu for cheap "is running" checks.
		syncLifecycleMu sync.Mutex
		syncCancel      context.CancelFunc
		syncWg          sync.WaitGroup
	}

	// registryEntry holds a registry client and its configuration.
	registryEntry struct {
		client       RegistryClient
		syncInterval time.Duration
		cacheTTL     time.Duration
		federation   *FederationConfig
	}

	// FederationConfig holds federation settings for a registry.
	FederationConfig struct {
		// Include patterns for namespaces to import.
		Include []string
		// Exclude patterns for namespaces to skip.
		Exclude []string
	}

	// RegistryClient defines the interface for registry operations.
	// Generated registry clients implement this interface.
	RegistryClient interface {
		// ListToolsets returns all available toolsets from the registry.
		ListToolsets(ctx context.Context) ([]*ToolsetInfo, error)
		// GetToolset retrieves the full schema for a specific toolset.
		GetToolset(ctx context.Context, name string) (*ToolsetSchema, error)
		// Search performs a semantic or keyword search on the registry.
		Search(ctx context.Context, query string) ([]*SearchResult, error)
	}

	// ToolsetInfo contains metadata about a toolset available in a registry.
	ToolsetInfo struct {
		// ID is the unique identifier for the toolset.
		ID string
		// Name is the human-readable name.
		Name string
		// Description provides details about the toolset.
		Description string
		// Version is the toolset version.
		Version string
		// Tags are metadata tags for discovery.
		Tags []string
		// Origin indicates the source registry for federated items.
		Origin string
	}

	// ToolsetSchema contains the full schema for a toolset including its tools.
	ToolsetSchema struct {
		// ID is the unique identifier for the toolset.
		ID string
		// Name is the human-readable name.
		Name string
		// Description provides details about the toolset.
		Description string
		// Version is the toolset version.
		Version string
		// Tools contains the tool definitions.
		Tools []*ToolSchema
		// Origin indicates the source registry for federated items.
		Origin string
	}

	// ToolSchema contains the schema for a single tool.
	ToolSchema struct {
		// Name is the tool identifier.
		Name string
		// Description explains what the tool does.
		Description string
		// Tags are optional metadata tags for discovery and filtering.
		Tags []string
		// PayloadSchema is the JSON Schema for tool input.
		PayloadSchema []byte
		// ResultSchema is the JSON Schema for tool output.
		ResultSchema []byte
		// SidecarSchema is the JSON Schema for tool sidecar (UI-only), when present.
		SidecarSchema []byte
	}

	// SearchResult contains a single search result from the registry.
	SearchResult struct {
		// ID is the unique identifier.
		ID string
		// Name is the human-readable name.
		Name string
		// Description provides details.
		Description string
		// Type indicates the result type (e.g., "tool", "toolset", "agent").
		Type string
		// SchemaRef is a reference to the full schema.
		SchemaRef string
		// RelevanceScore indicates how relevant this result is to the query.
		RelevanceScore float64
		// Tags are metadata tags.
		Tags []string
		// Origin indicates the federation source if applicable.
		Origin string
	}

	// Option configures a Manager.
	Option func(*Manager)
)

// WithCache sets the cache implementation for the manager.
func WithCache(c Cache) Option {
	return func(m *Manager) {
		m.cache = c
	}
}

// WithLogger sets the logger for the manager.
func WithLogger(l telemetry.Logger) Option {
	return func(m *Manager) {
		m.logger = l
	}
}

// WithMetrics sets the metrics recorder for the manager.
func WithMetrics(met telemetry.Metrics) Option {
	return func(m *Manager) {
		m.metrics = met
	}
}

// WithTracer sets the tracer for the manager.
func WithTracer(t telemetry.Tracer) Option {
	return func(m *Manager) {
		m.tracer = t
	}
}

// NewManager creates a new registry manager with the given options.
func NewManager(opts ...Option) *Manager {
	m := &Manager{
		registries: make(map[string]*registryEntry),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	// Use noop implementations if not provided
	if m.cache == nil {
		m.cache = &noopCache{}
	}
	if m.logger == nil {
		m.logger = &noopLogger{}
	}
	if m.metrics == nil {
		m.metrics = &noopMetrics{}
	}
	if m.tracer == nil {
		m.tracer = &noopTracer{}
	}
	// Create observability helper
	m.obs = NewObservability(m.logger, m.metrics, m.tracer)
	return m
}

// RegistryConfig holds configuration for adding a registry to the manager.
type RegistryConfig struct {
	// SyncInterval specifies how often to refresh the registry catalog.
	SyncInterval time.Duration
	// CacheTTL specifies local cache duration for registry data.
	CacheTTL time.Duration
	// Federation configures external registry import settings.
	Federation *FederationConfig
}

// AddRegistry registers a registry client with the manager.
func (m *Manager) AddRegistry(name string, client RegistryClient, cfg RegistryConfig) {
	ctx := context.Background()
	start := time.Now()

	m.mu.Lock()
	m.registries[name] = &registryEntry{
		client:       client,
		syncInterval: cfg.SyncInterval,
		cacheTTL:     cfg.CacheTTL,
		federation:   cfg.Federation,
	}
	m.mu.Unlock()

	event := OperationEvent{
		Operation: OpRegister,
		Registry:  name,
		Duration:  time.Since(start),
		Outcome:   OutcomeSuccess,
	}
	m.obs.LogOperation(ctx, event)
	m.obs.RecordOperationMetrics(event)
}

// cacheKey generates a cache key for a toolset.
func cacheKey(registry, toolset string) string {
	return path.Join("registry", registry, "toolset", toolset)
}

// noopLogger is a no-op logger implementation.
type noopLogger struct{}

func (noopLogger) Debug(context.Context, string, ...any) {}
func (noopLogger) Info(context.Context, string, ...any)  {}
func (noopLogger) Warn(context.Context, string, ...any)  {}
func (noopLogger) Error(context.Context, string, ...any) {}

// noopMetrics is a no-op metrics implementation.
type noopMetrics struct{}

func (noopMetrics) IncCounter(string, float64, ...string)        {}
func (noopMetrics) RecordTimer(string, time.Duration, ...string) {}
func (noopMetrics) RecordGauge(string, float64, ...string)       {}

// noopCache is a no-op cache implementation.
type noopCache struct{}

func (noopCache) Get(context.Context, string) (*ToolsetSchema, error) {
	return nil, nil
}

func (noopCache) Set(context.Context, string, *ToolsetSchema, time.Duration) error {
	return nil
}

func (noopCache) Delete(context.Context, string) error {
	return nil
}

// matchGlob matches slash-separated registry names against glob patterns.
// * matches any sequence within one path segment; ** spans zero or more segments.
func matchGlob(pattern, name string) bool {
	return matchGlobSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchGlobSegments(pattern, name []string) bool {
	if len(pattern) == 0 {
		return len(name) == 0
	}
	if pattern[0] == "**" {
		return matchGlobSegments(pattern[1:], name) || len(name) > 0 && matchGlobSegments(pattern, name[1:])
	}
	if len(name) == 0 {
		return false
	}
	return matchGlobSegment(pattern[0], name[0]) && matchGlobSegments(pattern[1:], name[1:])
}

func matchGlobSegment(pattern, name string) bool {
	patternIndex := 0
	nameIndex := 0
	starIndex := -1
	starNameIndex := 0

	for nameIndex < len(name) {
		if patternIndex < len(pattern) && pattern[patternIndex] == '*' {
			starIndex = patternIndex
			patternIndex++
			starNameIndex = nameIndex
			continue
		}
		if patternIndex < len(pattern) && pattern[patternIndex] == name[nameIndex] {
			patternIndex++
			nameIndex++
			continue
		}
		if starIndex == -1 {
			return false
		}
		patternIndex = starIndex + 1
		starNameIndex++
		nameIndex = starNameIndex
	}
	for patternIndex < len(pattern) && pattern[patternIndex] == '*' {
		patternIndex++
	}
	return patternIndex == len(pattern)
}
