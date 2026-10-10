package clientinfra_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"

	runstoremongo "github.com/CaliLuke/loom-mcp/v2/features/runstore/mongo"
	runstoreclient "github.com/CaliLuke/loom-mcp/v2/features/runstore/mongo/clients/mongo"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/runstoretest"
)

func TestMongoDriverV2RunstoreConformance(t *testing.T) {
	for _, deployment := range []struct {
		name string
		kind mongoDeployment
	}{
		{"replica_set", mongoReplicaSet}, {"standalone", mongoStandalone},
	} {
		t.Run(deployment.name, func(t *testing.T) {
			var driver *mongodriver.Client
			var database string
			if deployment.kind == mongoReplicaSet {
				driver, database = newMongoIntegrationClient(t)
			} else {
				driver, database, _ = newMongoIntegrationClientFor(t, deployment.kind)
			}
			sequence := 0
			runstoretest.RunConformance(t, func(t *testing.T) runstore.Store {
				sequence++
				prefix := fmt.Sprintf("conformance_%d", sequence)
				client, err := runstoreclient.New(runstoreclient.Options{
					Client: driver, Database: database, Timeout: 30 * time.Second,
					ContentCollection: prefix + "_content", CallCollection: prefix + "_calls", CounterCollection: prefix + "_sequences",
				})
				require.NoError(t, err)
				require.Equal(t, "runstore-mongo", client.Name())
				require.NoError(t, client.Ping(t.Context()))
				store, err := runstoremongo.NewStore(client)
				require.NoError(t, err)
				return store
			})
		})
	}
}
