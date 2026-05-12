package clientinfra_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	runlogclient "github.com/CaliLuke/loom-mcp/features/runlog/mongo/clients/mongo"
	sessionclient "github.com/CaliLuke/loom-mcp/features/session/mongo/clients/mongo"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/runtime/agent/session"
)

const integrationSessionID = "session-1"

func TestMongoDriverV2RunlogAppendRoundTrip(t *testing.T) {
	mongoClient, database := newMongoIntegrationClient(t)
	client, err := runlogclient.New(runlogclient.Options{
		Client:   mongoClient,
		Database: database,
		Timeout:  10 * time.Second,
	})
	require.NoError(t, err)

	ctx := context.Background()
	event := &runlog.Event{
		EventKey:  "evt-1",
		RunID:     "run-1",
		AgentID:   "agent-1",
		SessionID: integrationSessionID,
		TurnID:    "turn-1",
		Type:      hooks.RunStarted,
		Payload:   []byte(`{"ok":true}`),
		Timestamp: time.Unix(1, 0).UTC(),
	}
	first, err := client.Append(ctx, event)
	require.NoError(t, err)
	require.True(t, first.Inserted)
	require.NotEmpty(t, first.ID)
	require.Equal(t, first.ID, event.ID)

	duplicate := *event
	duplicate.ID = ""
	second, err := client.Append(ctx, &duplicate)
	require.NoError(t, err)
	require.False(t, second.Inserted)
	require.Equal(t, first.ID, second.ID)

	page, err := client.List(ctx, event.RunID, "", 10)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, first.ID, page.Events[0].ID)
	require.Equal(t, event.Payload, page.Events[0].Payload)
}

func TestMongoDriverV2SessionLinkChildRunTransaction(t *testing.T) {
	mongoClient, database := newMongoIntegrationClient(t)
	client, err := sessionclient.New(sessionclient.Options{
		Client:   mongoClient,
		Database: database,
		Timeout:  10 * time.Second,
	})
	require.NoError(t, err)

	ctx := context.Background()
	now := time.Unix(1, 0).UTC()
	require.NoError(t, client.UpsertRun(ctx, session.RunMeta{
		RunID:     "run-parent",
		AgentID:   "agent-parent",
		SessionID: integrationSessionID,
		Status:    session.RunStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}))

	child := session.RunMeta{
		RunID:     "run-child",
		AgentID:   "agent-child",
		SessionID: integrationSessionID,
		Status:    session.RunStatusPending,
		StartedAt: now,
		UpdatedAt: now,
	}
	require.NoError(t, client.LinkChildRun(ctx, "run-parent", child))

	parent, err := client.LoadRun(ctx, "run-parent")
	require.NoError(t, err)
	require.Equal(t, []string{"run-child"}, parent.ChildRunIDs)

	storedChild, err := client.LoadRun(ctx, "run-child")
	require.NoError(t, err)
	require.Equal(t, child.AgentID, storedChild.AgentID)
	require.Equal(t, child.SessionID, storedChild.SessionID)
}

func newMongoIntegrationClient(t *testing.T) (*mongodriver.Client, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	req := testcontainers.ContainerRequest{
		Image:        "mongo:7",
		Cmd:          []string{"mongod", "--replSet", "rs0", "--bind_ip_all"},
		ExposedPorts: []string{"27017/tcp"},
		WaitingFor:   wait.ForListeningPort("27017/tcp").WithStartupTimeout(time.Minute),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Skipf("Mongo testcontainer unavailable: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	initMongoReplicaSet(ctx, t, container)

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "27017")
	require.NoError(t, err)
	uri := fmt.Sprintf("mongodb://%s:%s/?directConnection=true&replicaSet=rs0", host, port.Port())

	client, err := mongodriver.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(10 * time.Second))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.Disconnect(context.Background())
	})
	require.NoError(t, client.Ping(ctx, nil))

	database := "loom_mcp_" + sanitizeDatabaseName(t.Name())
	t.Cleanup(func() {
		_ = client.Database(database).Drop(context.Background())
	})
	return client, database
}

func initMongoReplicaSet(ctx context.Context, t *testing.T, container testcontainers.Container) {
	t.Helper()
	initScript := `try {
  rs.initiate({_id: "rs0", members: [{_id: 0, host: "127.0.0.1:27017"}]})
} catch (e) {
  if (e.codeName !== "AlreadyInitialized") {
    throw e
  }
}`
	exitCode, output, err := container.Exec(ctx, []string{"mongosh", "--quiet", "--eval", initScript})
	require.NoError(t, err)
	if exitCode != 0 {
		body, _ := io.ReadAll(output)
		t.Fatalf("failed to initiate Mongo replica set: exit %d: %s", exitCode, body)
	}

	waitScript := `const deadline = Date.now() + 30000
while (!db.hello().isWritablePrimary) {
  if (Date.now() > deadline) {
    throw new Error("timed out waiting for writable primary")
  }
  sleep(100)
}`
	exitCode, output, err = container.Exec(ctx, []string{"mongosh", "--quiet", "--eval", waitScript})
	require.NoError(t, err)
	if exitCode != 0 {
		body, _ := io.ReadAll(output)
		t.Fatalf("Mongo replica set did not become primary: exit %d: %s", exitCode, body)
	}
}

func sanitizeDatabaseName(name string) string {
	replacer := strings.NewReplacer("/", "_", "-", "_", " ", "_")
	return replacer.Replace(name)
}
