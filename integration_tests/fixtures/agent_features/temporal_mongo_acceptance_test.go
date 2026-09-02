package agentfeatures_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"

	runlogmongo "github.com/CaliLuke/loom-mcp/v2/features/runlog/mongo"
	runlogclient "github.com/CaliLuke/loom-mcp/v2/features/runlog/mongo/clients/mongo"
	sessionmongo "github.com/CaliLuke/loom-mcp/v2/features/session/mongo"
	sessionclient "github.com/CaliLuke/loom-mcp/v2/features/session/mongo/clients/mongo"
	temporalengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	runDockerIntegrationEnv     = "LOOM_MCP_RUN_DOCKER_TESTS"
	requireDockerIntegrationEnv = "LOOM_MCP_REQUIRE_DOCKER_TESTS"
)

func TestGeneratedFeatureRealTemporalMongoWorkerReplacement(t *testing.T) {
	if !dockerIntegrationRequested() {
		t.Skipf("set %s=1 to run real persistence replacement contracts", runDockerIntegrationEnv)
	}
	uri := startMongoReplicaSet(t)
	server, err := testsuite.StartDevServer(t.Context(), testsuite.DevServerOptions{
		CachedDownload: testsuite.CachedDownload{Version: temporalCLIVersion},
		ClientOptions: &client.Options{
			DataConverter: temporalengine.NewAgentDataConverter(nil),
			Logger:        silentTemporalLogger{},
		},
		LogLevel: "error",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		server.Client().Close()
		require.NoError(t, server.Stop())
	})

	firstSessions, firstRunEvents, closeFirst := newMongoPersistenceStores(t, uri, "agent_features_temporal")
	testTemporalWorkerReplacement(
		t,
		server.Client(),
		firstSessions,
		firstRunEvents,
		func() (session.Store, runlog.Store) {
			replacementSessions, replacementRunEvents, _ := newMongoPersistenceStores(t, uri, "agent_features_temporal")
			return replacementSessions, replacementRunEvents
		},
		closeFirst,
	)
}

func newMongoPersistenceStores(t *testing.T, uri, database string) (session.Store, runlog.Store, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	mongoClient, err := mongo.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(10 * time.Second))
	require.NoError(t, err)
	require.NoError(t, mongoClient.Ping(ctx, nil))
	var closeOnce sync.Once
	closeClient := func() {
		closeOnce.Do(func() {
			require.NoError(t, mongoClient.Disconnect(context.Background()))
		})
	}
	t.Cleanup(closeClient)

	sessionsClient, err := sessionclient.New(sessionclient.Options{
		Client:   mongoClient,
		Database: database,
		Timeout:  10 * time.Second,
	})
	require.NoError(t, err)
	sessions, err := sessionmongo.NewStore(sessionsClient)
	require.NoError(t, err)
	runEventsClient, err := runlogclient.New(runlogclient.Options{
		Client:   mongoClient,
		Database: database,
		Timeout:  10 * time.Second,
	})
	require.NoError(t, err)
	runEvents, err := runlogmongo.NewStore(runEventsClient)
	require.NoError(t, err)
	return sessions, runEvents, closeClient
}

func startMongoReplicaSet(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "mongo:7",
			Cmd:          []string{"mongod", "--replSet", "rs0", "--bind_ip_all"},
			ExposedPorts: []string{"27017/tcp"},
			WaitingFor:   wait.ForListeningPort("27017/tcp").WithStartupTimeout(time.Minute),
		},
		Started: true,
	})
	if err != nil {
		if os.Getenv(requireDockerIntegrationEnv) == "1" {
			require.NoError(t, err, "Mongo testcontainer is required")
		}
		t.Skipf("Mongo testcontainer unavailable: %v", err)
	}
	t.Cleanup(func() {
		require.NoError(t, container.Terminate(context.Background()))
	})

	initScript := `try {
  rs.initiate({_id: "rs0", members: [{_id: 0, host: "127.0.0.1:27017"}]})
} catch (e) {
  if (e.codeName !== "AlreadyInitialized") {
    throw e
  }
}`
	execMongoScript(t, ctx, container, initScript, "failed to initiate Mongo replica set")
	waitScript := `const deadline = Date.now() + 30000
while (!db.hello().isWritablePrimary) {
  if (Date.now() > deadline) {
    throw new Error("timed out waiting for writable primary")
  }
  sleep(100)
}`
	execMongoScript(t, ctx, container, waitScript, "Mongo replica set did not become primary")

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "27017")
	require.NoError(t, err)
	return fmt.Sprintf("mongodb://%s:%s/?directConnection=true&replicaSet=rs0", host, port.Port())
}

func execMongoScript(t *testing.T, ctx context.Context, container testcontainers.Container, script, message string) {
	t.Helper()
	exitCode, output, err := container.Exec(ctx, []string{"mongosh", "--quiet", "--eval", script})
	require.NoError(t, err)
	if exitCode == 0 {
		return
	}
	body, readErr := io.ReadAll(output)
	require.NoError(t, readErr)
	t.Fatalf("%s: exit %d: %s", message, exitCode, body)
}

func dockerIntegrationRequested() bool {
	return os.Getenv(runDockerIntegrationEnv) == "1" || os.Getenv(requireDockerIntegrationEnv) == "1"
}
