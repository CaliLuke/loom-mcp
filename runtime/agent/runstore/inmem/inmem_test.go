package inmem_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/runstoretest"
)

func TestConformance(t *testing.T) {
	runstoretest.RunConformance(t, func(t *testing.T) runstore.Store {
		return inmem.New()
	})
}

func TestContentHasNoMongoSizeLimit(t *testing.T) {
	store := inmem.New()
	data := make([]byte, 16*1024*1024)
	hash, err := store.Put(t.Context(), "scope", data)
	require.NoError(t, err)
	got, err := store.Get(t.Context(), "scope", hash)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}
