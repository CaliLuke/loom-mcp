package registry

import (
	"context"
	"sync"
	"testing"

	genregistry "github.com/CaliLuke/loom-mcp/registry/gen/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolsetCatalogSaveGetDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cat := newToolsetCatalog(newTestCatalogMap())

	toolset := &genregistry.Toolset{
		Name:         "atlas.read",
		Tags:         []string{"atlas", "read"},
		RegisteredAt: "2026-03-16T12:00:00Z",
		Tools: []*genregistry.ToolSchema{
			{
				Name:          "atlas.read.get_time_series",
				PayloadSchema: []byte(`{"type":"object"}`),
				ResultSchema:  []byte(`{"type":"object"}`),
			},
		},
	}

	require.NoError(t, cat.SaveToolset(ctx, toolset))

	got, err := cat.GetToolset(ctx, toolset.Name)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, toolset.Name, got.Name)
	assert.Equal(t, toolset.RegisteredAt, got.RegisteredAt)
	assert.Equal(t, toolset.Tags, got.Tags)

	require.NoError(t, cat.DeleteToolset(ctx, toolset.Name))

	_, err = cat.GetToolset(ctx, toolset.Name)
	require.ErrorIs(t, err, errToolsetNotFound)

	err = cat.DeleteToolset(ctx, toolset.Name)
	require.ErrorIs(t, err, errToolsetNotFound)
}

func TestToolsetCatalogSaveRotatesRegistrationToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backingMap := newTestCatalogMap()
	cat := newToolsetCatalog(backingMap)
	toolset := testCatalogToolset("atlas.read", "Atlas reads", []string{"atlas", "read"})

	require.NoError(t, cat.SaveToolset(ctx, toolset))
	firstRaw, ok := backingMap.Get(toolsetCatalogKey(toolset.Name))
	require.True(t, ok)
	firstEntry, err := parseCatalogEntry(toolset.Name, firstRaw)
	require.NoError(t, err)

	require.NoError(t, cat.SaveToolset(ctx, toolset))
	secondRaw, ok := backingMap.Get(toolsetCatalogKey(toolset.Name))
	require.True(t, ok)
	secondEntry, err := parseCatalogEntry(toolset.Name, secondRaw)
	require.NoError(t, err)

	assert.NotEmpty(t, firstEntry.RegistrationToken)
	assert.NotEmpty(t, secondEntry.RegistrationToken)
	assert.NotEqual(t, firstEntry.RegistrationToken, secondEntry.RegistrationToken)
	assert.Equal(t, toolset.Name, secondEntry.Toolset.Name)
	assert.Equal(t, toolset.RegisteredAt, secondEntry.Toolset.RegisteredAt)
}

func TestToolsetCatalogListToolsetsFiltersTags(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cat := newToolsetCatalog(newTestCatalogMap())
	require.NoError(t, cat.SaveToolset(ctx, testCatalogToolset("atlas.read", "Atlas reads", []string{"atlas", "read"})))
	require.NoError(t, cat.SaveToolset(ctx, testCatalogToolset("atlas.write", "Atlas writes", []string{"atlas", "write"})))
	require.NoError(t, cat.SaveToolset(ctx, testCatalogToolset("grafana.read", "Grafana reads", []string{"grafana", "read"})))

	got, err := cat.ListToolsets(ctx, []string{"atlas", "read"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "atlas.read", got[0].Name)
}

func TestToolsetCatalogListAndSearchToolsetsSortByName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cat := newToolsetCatalog(newTestCatalogMap())
	require.NoError(t, cat.SaveToolset(ctx, testCatalogToolset("zulu.read", "Readable data", []string{"data"})))
	require.NoError(t, cat.SaveToolset(ctx, testCatalogToolset("atlas.read", "Readable data", []string{"data"})))
	require.NoError(t, cat.SaveToolset(ctx, testCatalogToolset("midgard.read", "Readable data", []string{"data"})))

	listed, err := cat.ListToolsets(ctx, []string{"data"})
	require.NoError(t, err)
	assert.Equal(t, []string{"atlas.read", "midgard.read", "zulu.read"}, catalogToolsetNames(listed))

	searched, err := cat.SearchToolsets(ctx, "read")
	require.NoError(t, err)
	assert.Equal(t, []string{"atlas.read", "midgard.read", "zulu.read"}, catalogToolsetNames(searched))
}

func TestToolsetCatalogSearchToolsetsMatchesNameDescriptionAndTags(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cat := newToolsetCatalog(newTestCatalogMap())
	require.NoError(t, cat.SaveToolset(ctx, testCatalogToolset("atlas.read", "Reads Atlas time series", []string{"atlas", "signals"})))
	require.NoError(t, cat.SaveToolset(ctx, testCatalogToolset("grafana.read", "Reads dashboards", []string{"dashboards"})))

	t.Run("matches name", func(t *testing.T) {
		got, err := cat.SearchToolsets(ctx, "atlas")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "atlas.read", got[0].Name)
	})

	t.Run("matches description", func(t *testing.T) {
		got, err := cat.SearchToolsets(ctx, "dashboards")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "grafana.read", got[0].Name)
	})

	t.Run("matches tags case insensitively", func(t *testing.T) {
		got, err := cat.SearchToolsets(ctx, "SIGNALS")
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "atlas.read", got[0].Name)
	})
}

func TestToolsetCatalogListAndSearchSkipConcurrentlyDeletedEntries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backing := newTestCatalogMap()
	cat := newToolsetCatalog(&phantomKeyCatalogMap{
		testCatalogMap: backing,
		phantomKey:     toolsetCatalogKey("vanished.read"),
	})
	require.NoError(t, cat.SaveToolset(ctx, testCatalogToolset("atlas.read", "Readable data", []string{"data"})))

	listed, err := cat.ListToolsets(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"atlas.read"}, catalogToolsetNames(listed))

	searched, err := cat.SearchToolsets(ctx, "read")
	require.NoError(t, err)
	assert.Equal(t, []string{"atlas.read"}, catalogToolsetNames(searched))
}

func TestToolsetCatalogListAndSearchSkipUndecodableEntries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backing := newTestCatalogMap()
	cat := newToolsetCatalog(backing)
	require.NoError(t, cat.SaveToolset(ctx, testCatalogToolset("atlas.read", "Readable data", []string{"data"})))

	corrupt := []struct {
		name string
		body string
	}{
		{name: "corrupt.json", body: `{not json`},
		{name: "corrupt.token", body: `{"toolset":{"name":"corrupt.token","registered_at":"2026-03-16T12:00:00Z"}}`},
	}
	for _, entry := range corrupt {
		_, err := backing.Set(ctx, toolsetCatalogKey(entry.name), entry.body)
		require.NoError(t, err)
	}

	listed, err := cat.ListToolsets(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"atlas.read"}, catalogToolsetNames(listed))

	searched, err := cat.SearchToolsets(ctx, "corrupt")
	require.NoError(t, err)
	assert.Empty(t, searched)

	// Direct reads stay fail-fast on undecodable entries.
	for _, entry := range corrupt {
		_, err := cat.GetToolset(ctx, entry.name)
		require.Error(t, err)
		assert.NotErrorIs(t, err, errToolsetNotFound)
	}
}

type testCatalogMap struct {
	mu     sync.RWMutex
	values map[string]string
}

func newTestCatalogMap() *testCatalogMap {
	return &testCatalogMap{values: make(map[string]string)}
}

func (m *testCatalogMap) Delete(ctx context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := m.values[key]
	delete(m.values, key)
	return prev, nil
}

func (m *testCatalogMap) Get(key string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.values[key]
	return val, ok
}

func (m *testCatalogMap) Keys() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.values))
	for key := range m.values {
		keys = append(keys, key)
	}
	return keys
}

func (m *testCatalogMap) Set(ctx context.Context, key, value string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := m.values[key]
	m.values[key] = value
	return prev, nil
}

func (m *testCatalogMap) SetAndWait(ctx context.Context, key, value string) (string, error) {
	return m.Set(ctx, key, value)
}

// phantomKeyCatalogMap reports one extra key from Keys() that Get never
// resolves, simulating an entry deleted by another node between the Keys()
// snapshot and the per-key read.
type phantomKeyCatalogMap struct {
	*testCatalogMap
	phantomKey string
}

func (m *phantomKeyCatalogMap) Keys() []string {
	return append(m.testCatalogMap.Keys(), m.phantomKey)
}

func testCatalogToolset(name string, description string, tags []string) *genregistry.Toolset {
	return &genregistry.Toolset{
		Name:         name,
		Description:  &description,
		Tags:         tags,
		RegisteredAt: "2026-03-16T12:00:00Z",
		Tools: []*genregistry.ToolSchema{
			{
				Name:          name + ".tool",
				PayloadSchema: []byte(`{"type":"object"}`),
				ResultSchema:  []byte(`{"type":"object"}`),
			},
		},
	}
}

func catalogToolsetNames(toolsets []*genregistry.Toolset) []string {
	names := make([]string, len(toolsets))
	for i, toolset := range toolsets {
		names[i] = toolset.Name
	}
	return names
}
