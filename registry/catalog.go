// Package registry owns the toolset catalog used by the gateway. The catalog is
// persisted directly in the registry replicated-map keyspace so all registry
// nodes share one canonical view of registration state.
package registry

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"sort"
	"strings"

	"uuid"

	genregistry "github.com/CaliLuke/loom-mcp/v2/registry/gen/registry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
)

type (
	// catalogMap captures the replicated-map operations the registry catalog needs.
	// The concrete production implementation is `*rmap.Map`; tests use small in-memory fakes.
	catalogMap interface {
		Delete(ctx context.Context, key string) (string, error)
		Get(key string) (string, bool)
		Keys() []string
		Set(ctx context.Context, key, value string) (string, error)
		SetAndWait(ctx context.Context, key, value string) (string, error)
	}

	// catalogEntry is the canonical persisted registry record.
	// The transport-facing toolset metadata stays separate from the internal
	// registration token so wall-clock timestamps are never treated as identity.
	catalogEntry struct {
		Toolset           *genregistry.Toolset `json:"toolset"`
		RegistrationToken string               `json:"registration_token"`
	}

	// toolsetCatalog persists toolsets in the registry replicated-map keyspace.
	// Entries are JSON encoded so every read returns a fresh value detached from
	// caller-owned memory and durable across process restarts.
	toolsetCatalog struct {
		m      catalogMap
		logger telemetry.Logger
	}
)

const toolsetCatalogKeyPrefix = "registry:toolset:"

var errToolsetNotFound = errors.New("toolset not found")

// newToolsetCatalog constructs the canonical registry catalog over the provided
// replicated map. The caller owns the map lifecycle.
func newToolsetCatalog(m catalogMap) *toolsetCatalog {
	return newToolsetCatalogWithLogger(m, nil)
}

// newToolsetCatalogWithLogger constructs the catalog with an explicit logger
// for skipped-entry diagnostics during list-style reads. A nil logger falls
// back to a no-op logger.
func newToolsetCatalogWithLogger(m catalogMap, logger telemetry.Logger) *toolsetCatalog {
	if logger == nil {
		logger = telemetry.NewNoopLogger()
	}
	return &toolsetCatalog{m: m, logger: logger}
}

// SaveToolset stores or replaces a toolset entry under its deterministic catalog key.
func (c *toolsetCatalog) SaveToolset(ctx context.Context, toolset *genregistry.Toolset) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entry := catalogEntry{
		Toolset:           toolset,
		RegistrationToken: uuid.New().String(),
	}
	body, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal toolset %q: %w", toolset.Name, err)
	}
	if _, err := c.m.SetAndWait(ctx, toolsetCatalogKey(toolset.Name), string(body)); err != nil {
		return fmt.Errorf("store toolset %q: %w", toolset.Name, err)
	}
	return nil
}

// GetToolset loads and decodes a toolset by name. Missing entries return
// errToolsetNotFound so callers can map absence to the transport contract.
func (c *toolsetCatalog) GetToolset(ctx context.Context, name string) (*genregistry.Toolset, error) {
	entry, err := c.entry(ctx, name)
	if err != nil {
		return nil, err
	}
	return entry.Toolset, nil
}

// RegistrationToken loads the current registration epoch token for a toolset.
// The token changes on every save so same-name re-registration invalidates old
// health records and stale pongs.
func (c *toolsetCatalog) RegistrationToken(ctx context.Context, name string) (string, error) {
	entry, err := c.entry(ctx, name)
	if err != nil {
		return "", err
	}
	return entry.RegistrationToken, nil
}

// DeleteToolset removes a toolset entry. Deleting a missing toolset returns
// errToolsetNotFound so unregister can surface a precise not-found error.
func (c *toolsetCatalog) DeleteToolset(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key := toolsetCatalogKey(name)
	if _, ok := c.m.Get(key); !ok {
		return errToolsetNotFound
	}
	if _, err := c.m.Delete(ctx, key); err != nil {
		return fmt.Errorf("delete toolset %q: %w", name, err)
	}
	return nil
}

// ListToolsets returns every catalog entry whose tags satisfy the filter.
func (c *toolsetCatalog) ListToolsets(ctx context.Context, tags []string) ([]*genregistry.Toolset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	keys := c.m.Keys()
	sort.Strings(keys)
	toolsets := make([]*genregistry.Toolset, 0, len(keys))
	for _, key := range keys {
		if !strings.HasPrefix(key, toolsetCatalogKeyPrefix) {
			continue
		}
		toolset, err := c.getToolsetForScan(ctx, strings.TrimPrefix(key, toolsetCatalogKeyPrefix))
		if err != nil {
			return nil, err
		}
		if toolset == nil {
			continue
		}
		if catalogMatchesTags(toolset.Tags, tags) {
			toolsets = append(toolsets, toolset)
		}
	}
	sortToolsetsByName(toolsets)
	return toolsets, nil
}

// SearchToolsets returns catalog entries whose name, description, or tags match
// the query case-insensitively.
func (c *toolsetCatalog) SearchToolsets(ctx context.Context, query string) ([]*genregistry.Toolset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lowerQuery := strings.ToLower(query)
	keys := c.m.Keys()
	sort.Strings(keys)
	toolsets := make([]*genregistry.Toolset, 0, len(keys))
	for _, key := range keys {
		if !strings.HasPrefix(key, toolsetCatalogKeyPrefix) {
			continue
		}
		toolset, err := c.getToolsetForScan(ctx, strings.TrimPrefix(key, toolsetCatalogKeyPrefix))
		if err != nil {
			return nil, err
		}
		if toolset == nil {
			continue
		}
		if catalogMatchesQuery(toolset, lowerQuery) {
			toolsets = append(toolsets, toolset)
		}
	}
	sortToolsetsByName(toolsets)
	return toolsets, nil
}

// getToolsetForScan loads one toolset during a catalog-wide scan (List/Search).
// It returns a nil toolset without error when the entry vanished between
// Keys() and Get (concurrent unregister on another node) or when the stored
// record does not decode, so one deleted or corrupt entry cannot fail the
// whole scan. Direct GetToolset reads stay fail-fast. Context errors still
// abort the scan.
func (c *toolsetCatalog) getToolsetForScan(ctx context.Context, name string) (*genregistry.Toolset, error) {
	toolset, err := c.GetToolset(ctx, name)
	if err == nil {
		return toolset, nil
	}
	if errors.Is(err, errToolsetNotFound) {
		return nil, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	c.logger.Error(ctx, "skipping undecodable catalog entry",
		"component", "tool-registry-catalog",
		"toolset", name,
		"err", err,
	)
	return nil, nil
}

// entry loads and validates the canonical persisted catalog record for a toolset.
func (c *toolsetCatalog) entry(ctx context.Context, name string) (catalogEntry, error) {
	if err := ctx.Err(); err != nil {
		return catalogEntry{}, err
	}
	body, ok := c.m.Get(toolsetCatalogKey(name))
	if !ok {
		return catalogEntry{}, errToolsetNotFound
	}
	return parseCatalogEntry(name, body)
}

// parseCatalogEntry decodes one catalog record and rejects incomplete payloads
// from the shared map so callers never mistake malformed state for a valid
// registration.
func parseCatalogEntry(name string, body string) (catalogEntry, error) {
	var entry catalogEntry
	if err := json.Unmarshal([]byte(body), &entry); err != nil {
		return catalogEntry{}, fmt.Errorf("unmarshal toolset %q: %w", name, err)
	}
	if entry.Toolset == nil {
		return catalogEntry{}, fmt.Errorf("toolset %q missing toolset payload", name)
	}
	if entry.RegistrationToken == "" {
		return catalogEntry{}, fmt.Errorf("toolset %q missing registration token", name)
	}
	return entry, nil
}

// toolsetCatalogKey returns the deterministic replicated-map key for a toolset.
func toolsetCatalogKey(name string) string {
	return toolsetCatalogKeyPrefix + name
}

// catalogMatchesTags reports whether the toolset tags contain every requested
// filter tag.
func catalogMatchesTags(toolsetTags, filterTags []string) bool {
	if len(filterTags) == 0 {
		return true
	}
	toolsetTagSet := make(map[string]struct{}, len(toolsetTags))
	for _, tag := range toolsetTags {
		toolsetTagSet[tag] = struct{}{}
	}
	for _, tag := range filterTags {
		if _, ok := toolsetTagSet[tag]; !ok {
			return false
		}
	}
	return true
}

// catalogMatchesQuery reports whether the search query matches the toolset name,
// description, or tags case-insensitively.
func catalogMatchesQuery(toolset *genregistry.Toolset, lowerQuery string) bool {
	if strings.Contains(strings.ToLower(toolset.Name), lowerQuery) {
		return true
	}
	if toolset.Description != nil && strings.Contains(strings.ToLower(*toolset.Description), lowerQuery) {
		return true
	}
	for _, tag := range toolset.Tags {
		if strings.Contains(strings.ToLower(tag), lowerQuery) {
			return true
		}
	}
	return false
}

func sortToolsetsByName(toolsets []*genregistry.Toolset) {
	sort.Slice(toolsets, func(i, j int) bool {
		return toolsets[i].Name < toolsets[j].Name
	})
}
