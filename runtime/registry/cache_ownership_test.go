package registry

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryCacheSchemaOwnership(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		name := "set"
		if refresh {
			name = "refresh"
		}
		t.Run(name, func(t *testing.T) {
			input := ownershipSchema()
			cache := NewMemoryCache(WithRefreshFunc(func(context.Context, string) (*ToolsetSchema, error) {
				return input, nil
			}))
			if refresh {
				require.NoError(t, cache.Set(t.Context(), "key", &ToolsetSchema{ID: "old"}, time.Hour))
				cache.StartRefresh(t.Context())
				defer cache.StopRefresh()
				cache.triggerRefresh("key")
				require.Eventually(t, func() bool {
					got, err := cache.Get(t.Context(), "key")
					return err == nil && got != nil && got.ID == "catalog"
				}, 5*time.Second, time.Millisecond)
				cache.StopRefresh()
			} else {
				require.NoError(t, cache.Set(t.Context(), "key", input, time.Hour))
			}
			mutateOwnershipSchema(input)
			first, err := cache.Get(t.Context(), "key")
			require.NoError(t, err)
			require.Equal(t, ownershipSchema(), first)
			mutateOwnershipSchema(first)
			second, err := cache.Get(t.Context(), "key")
			require.NoError(t, err)
			assert.Equal(t, ownershipSchema(), second)
		})
	}
}

func TestMemoryCachePreservesNilSchema(t *testing.T) {
	cache := NewMemoryCache()
	require.NoError(t, cache.Set(t.Context(), "nil", nil, time.Hour))
	got, err := cache.Get(t.Context(), "nil")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func ownershipSchema() *ToolsetSchema {
	return &ToolsetSchema{ID: "catalog", Tools: []*ToolSchema{
		{Name: "tool", Tags: []string{"tag"}, PayloadSchema: []byte(`{"type":"object"}`), ResultSchema: []byte(`{"type":"string"}`), SidecarSchema: []byte(`{"type":"number"}`)},
		nil,
		{Name: "empty", Tags: []string{}, PayloadSchema: []byte{}},
	}}
}

func mutateOwnershipSchema(schema *ToolsetSchema) {
	schema.ID = "changed"
	schema.Tools[0].Name = "changed"
	schema.Tools[0].Tags[0] = "changed"
	schema.Tools[0].PayloadSchema[0] = 'x'
	schema.Tools[0].ResultSchema[0] = 'x'
	schema.Tools[0].SidecarSchema[0] = 'x'
	schema.Tools[1] = &ToolSchema{Name: "replaced"}
}
