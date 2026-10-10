package mongo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runstoremongo "github.com/CaliLuke/loom-mcp/v2/features/runstore/mongo"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/runstoretest"
)

type fakeClient struct {
	runstore.Store
}

func TestNewStoreRejectsNil(t *testing.T) {
	_, err := runstoremongo.NewStore(nil)
	assert.Error(t, err)
}

func TestStoreDelegation(t *testing.T) {
	runstoretest.RunConformance(t, func(t *testing.T) runstore.Store {
		s, err := runstoremongo.NewStore(fakeClient{Store: inmem.New()})
		require.NoError(t, err)
		return s
	})
}

func (fakeClient) Name() string {
	return "fake"
}

func (fakeClient) Ping(context.Context) error {
	return nil
}
