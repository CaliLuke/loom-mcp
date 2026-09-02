package clientinfra_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	memoryclient "github.com/CaliLuke/loom-mcp/v2/features/memory/mongo/clients/mongo"
	promptclient "github.com/CaliLuke/loom-mcp/v2/features/prompt/mongo/clients/mongo"
	runlogclient "github.com/CaliLuke/loom-mcp/v2/features/runlog/mongo/clients/mongo"
	sessionclient "github.com/CaliLuke/loom-mcp/v2/features/session/mongo/clients/mongo"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
)

const (
	integrationSessionID        = "session-1"
	runDockerIntegrationEnv     = "LOOM_MCP_RUN_DOCKER_TESTS"
	requireDockerIntegrationEnv = "LOOM_MCP_REQUIRE_DOCKER_TESTS"
)

func TestDockerIntegrationRequested(t *testing.T) {
	for _, tc := range []struct {
		name     string
		run      string
		required string
		want     bool
	}{
		{name: "unset", want: false},
		{name: "disabled", run: "0", required: "0", want: false},
		{name: "selected", run: "1", want: true},
		{name: "required implies selected", required: "1", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(runDockerIntegrationEnv, tc.run)
			t.Setenv(requireDockerIntegrationEnv, tc.required)
			require.Equal(t, tc.want, dockerIntegrationRequested())
		})
	}
}

func TestMongoDriverV2RunlogAppendRoundTrip(t *testing.T) {
	mongoClient, database := newMongoIntegrationClient(t)
	client, err := runlogclient.New(runlogclient.Options{
		Client:   mongoClient,
		Database: database,
		Timeout:  10 * time.Second,
	})
	require.NoError(t, err)
	require.Equal(t, "runlog-mongo", client.Name())
	require.NoError(t, client.Ping(context.Background()))

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

	conflict := *event
	conflict.ID = ""
	conflict.Payload = []byte(`{"ok":false}`)
	_, err = client.Append(ctx, &conflict)
	require.ErrorContains(t, err, "conflicts with existing event body")

	for i := 2; i <= 4; i++ {
		next := *event
		next.ID = ""
		next.EventKey = fmt.Sprintf("evt-%d", i)
		next.Timestamp = time.Unix(int64(i), 0).UTC()
		_, err = client.Append(ctx, &next)
		require.NoError(t, err)
	}

	page, err := client.List(ctx, event.RunID, "", 2)
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	require.Equal(t, first.ID, page.Events[0].ID)
	require.Equal(t, event.Payload, page.Events[0].Payload)
	require.NotEmpty(t, page.NextCursor)
	require.Equal(t, "evt-2", page.Events[1].EventKey)

	nextPage, err := client.List(ctx, event.RunID, page.NextCursor, 2)
	require.NoError(t, err)
	require.Len(t, nextPage.Events, 2)
	require.Empty(t, nextPage.NextCursor)
	require.Equal(t, "evt-3", nextPage.Events[0].EventKey)
	require.Equal(t, "evt-4", nextPage.Events[1].EventKey)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = client.List(canceled, event.RunID, "", 1)
	require.ErrorIs(t, err, context.Canceled)
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
	parent := session.RunMeta{
		RunID:     "run-parent",
		AgentID:   "agent-parent",
		SessionID: integrationSessionID,
		Status:    session.RunStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}
	require.ErrorIs(t, client.UpsertRun(ctx, parent), session.ErrSessionNotFound)
	_, err = client.CreateSession(ctx, integrationSessionID, now)
	require.NoError(t, err)
	require.NoError(t, client.UpsertRun(ctx, parent))

	child := session.RunMeta{
		RunID:     "run-child",
		AgentID:   "agent-child",
		SessionID: integrationSessionID,
		Status:    session.RunStatusPending,
		StartedAt: now,
		UpdatedAt: now,
	}
	require.NoError(t, client.LinkChildRun(ctx, "run-parent", child))

	parent, err = client.LoadRun(ctx, "run-parent")
	require.NoError(t, err)
	require.Equal(t, []string{"run-child"}, parent.ChildRunIDs)

	storedChild, err := client.LoadRun(ctx, "run-child")
	require.NoError(t, err)
	require.Equal(t, child.AgentID, storedChild.AgentID)
	require.Equal(t, child.SessionID, storedChild.SessionID)

	_, err = client.EndSession(ctx, integrationSessionID, now.Add(time.Hour))
	require.NoError(t, err)
	storedChild.Status = session.RunStatusCanceled
	require.NoError(t, client.UpsertRun(ctx, storedChild))
	newRun := child
	newRun.RunID = "run-after-end"
	require.ErrorIs(t, client.UpsertRun(ctx, newRun), session.ErrSessionEnded)
}

func TestMongoDriverV2PromptResolutionRoundTrip(t *testing.T) {
	mongoClient, database := newMongoIntegrationClient(t)
	client, err := promptclient.New(promptclient.Options{
		Client:   mongoClient,
		Database: database,
		Timeout:  10 * time.Second,
	})
	require.NoError(t, err)

	ctx := context.Background()
	promptID := prompt.Ident("assistant.system")
	require.NoError(t, client.Set(ctx, promptID, prompt.Scope{}, "global", map[string]string{"source": "global"}))
	require.NoError(t, client.Set(ctx, promptID, prompt.Scope{Labels: map[string]string{"env": "prod"}}, "production", nil))
	require.NoError(t, client.Set(ctx, promptID, prompt.Scope{SessionID: integrationSessionID, Labels: map[string]string{"env": "prod"}}, "session", nil))

	override, err := client.Resolve(ctx, promptID, prompt.Scope{
		SessionID: integrationSessionID,
		Labels:    map[string]string{"env": "prod", "region": "us"},
	})
	require.NoError(t, err)
	require.NotNil(t, override)
	require.Equal(t, "session", override.Template)

	override, err = client.Resolve(ctx, promptID, prompt.Scope{Labels: map[string]string{"env": "prod", "region": "us"}})
	require.NoError(t, err)
	require.NotNil(t, override)
	require.Equal(t, "production", override.Template)

	override, err = client.Resolve(ctx, promptID, prompt.Scope{})
	require.NoError(t, err)
	require.NotNil(t, override)
	require.Equal(t, "global", override.Template)
	require.Equal(t, "global", override.Metadata["source"])

	history, err := client.History(ctx, promptID)
	require.NoError(t, err)
	require.Len(t, history, 3)
}

func TestMongoDriverV2MemoryRoundTrip(t *testing.T) {
	mongoClient, database := newMongoIntegrationClient(t)
	client, err := memoryclient.New(memoryclient.Options{
		Client:   mongoClient,
		Database: database,
		Timeout:  10 * time.Second,
	})
	require.NoError(t, err)

	ctx := context.Background()
	firstAt := time.Unix(2, 0).UTC()
	require.NoError(t, client.AppendEvents(ctx, "assistant", "run-1", []memory.Event{{
		Type:      memory.EventUserMessage,
		Timestamp: firstAt,
		Data:      map[string]any{"text": "hello", "position": 1},
		Labels:    map[string]string{"role": "user"},
	}}))
	require.NoError(t, client.AppendEvents(ctx, "assistant", "run-1", []memory.Event{{
		Type: memory.EventPlannerNote,
		Data: map[string]any{"nested": map[string]any{"ok": true}},
	}}))

	snapshot, err := client.LoadRun(ctx, "assistant", "run-1")
	require.NoError(t, err)
	require.Equal(t, "assistant", snapshot.AgentID)
	require.Equal(t, "run-1", snapshot.RunID)
	require.Len(t, snapshot.Events, 2)
	require.Equal(t, firstAt, snapshot.Events[0].Timestamp)
	require.Equal(t, map[string]string{"role": "user"}, snapshot.Events[0].Labels)
	require.Equal(t, map[string]any{"text": "hello", "position": int32(1)}, snapshot.Events[0].Data)
	require.Equal(t, map[string]any{"nested": map[string]any{"ok": true}}, snapshot.Events[1].Data)
	require.False(t, snapshot.Events[1].Timestamp.IsZero())
}

func TestMongoDriverV2LegacyUpgradeAndConcurrencyContracts(t *testing.T) {
	mongoClient, database := newMongoIntegrationClient(t)
	ctx := context.Background()

	t.Run("prompt fingerprint migration", func(t *testing.T) {
		collection := "prompt_upgrade"
		labels := map[string]string{"account": "acme", "region": "west"}
		promptID := prompt.Ident("assistant.legacy")
		_, err := mongoClient.Database(database).Collection(collection).InsertOne(ctx, bson.M{
			"prompt_id":         promptID.String(),
			"scope_session":     integrationSessionID,
			"scope_labels":      labels,
			"scope_label_count": len(labels),
			"template":          "legacy-template",
			"version":           prompt.VersionFromTemplate("legacy-template"),
			"created_at":        time.Unix(1, 0).UTC(),
		})
		require.NoError(t, err)

		client, err := promptclient.New(promptclient.Options{
			Client:     mongoClient,
			Database:   database,
			Collection: collection,
			Timeout:    10 * time.Second,
		})
		require.NoError(t, err)
		override, err := client.Resolve(ctx, promptID, prompt.Scope{
			SessionID: integrationSessionID,
			Labels:    map[string]string{"account": "acme", "region": "west", "tier": "gold"},
		})
		require.NoError(t, err)
		require.NotNil(t, override)
		require.Equal(t, "legacy-template", override.Template)

		var migrated bson.M
		err = mongoClient.Database(database).Collection(collection).FindOne(ctx, bson.M{"prompt_id": promptID.String()}).Decode(&migrated)
		require.NoError(t, err)
		require.Equal(t, prompt.ScopeFingerprint(labels), migrated["scope_fingerprint"])
	})

	t.Run("memory legacy and bucket ordering", func(t *testing.T) {
		legacyCollection := "memory_upgrade"
		eventsCollection := "memory_upgrade_events"
		equalAt := time.Unix(2, 0).UTC()
		_, err := mongoClient.Database(database).Collection(legacyCollection).InsertOne(ctx, bson.M{
			"agent_id": "assistant",
			"run_id":   "run-upgrade",
			"events": bson.A{bson.M{
				"type":      memory.EventUserMessage,
				"timestamp": equalAt,
				"data":      bson.M{"text": "legacy-equal"},
			}},
			"meta": bson.M{"source": "legacy"},
		})
		require.NoError(t, err)
		_, err = mongoClient.Database(database).Collection(eventsCollection).InsertMany(ctx, []any{
			bson.M{
				"_id":        mustObjectID(t, "000000000000000000000001"),
				"agent_id":   "assistant",
				"run_id":     "run-upgrade",
				"created_at": time.Unix(10, 0).UTC(),
				"events": bson.A{
					bson.M{"type": memory.EventPlannerNote, "timestamp": time.Unix(1, 0).UTC(), "data": bson.M{"text": "bucket-first"}},
					bson.M{"type": memory.EventAssistantMessage, "timestamp": time.Unix(3, 0).UTC(), "data": bson.M{"text": "bucket-last"}},
				},
			},
			bson.M{
				"_id":        mustObjectID(t, "000000000000000000000002"),
				"agent_id":   "assistant",
				"run_id":     "run-upgrade",
				"created_at": time.Unix(11, 0).UTC(),
				"events": bson.A{
					bson.M{"type": memory.EventToolResult, "timestamp": equalAt, "data": bson.M{"text": "bucket-equal"}},
				},
			},
		})
		require.NoError(t, err)

		client, err := memoryclient.New(memoryclient.Options{
			Client:           mongoClient,
			Database:         database,
			Collection:       legacyCollection,
			EventsCollection: eventsCollection,
			Timeout:          10 * time.Second,
		})
		require.NoError(t, err)
		snapshot, err := client.LoadRun(ctx, "assistant", "run-upgrade")
		require.NoError(t, err)
		require.Len(t, snapshot.Events, 4)
		require.Equal(t, "legacy", snapshot.Meta["source"])
		texts := make([]string, len(snapshot.Events))
		for i, event := range snapshot.Events {
			texts[i] = event.Data.(map[string]any)["text"].(string)
		}
		require.Equal(t, []string{"bucket-first", "legacy-equal", "bucket-equal", "bucket-last"}, texts)
	})

	t.Run("runlog duplicate insertion and index conflict", func(t *testing.T) {
		collection := "runlog_upgrade"
		_, err := mongoClient.Database(database).Collection(collection).InsertOne(ctx, bson.M{
			"event_key":  "legacy-event",
			"run_id":     "run-upgrade",
			"agent_id":   "assistant",
			"session_id": integrationSessionID,
			"turn_id":    "turn-legacy",
			"type":       hooks.RunStarted,
			"payload":    []byte(`{"legacy":true}`),
			"timestamp":  time.Unix(1, 0).UTC(),
		})
		require.NoError(t, err)
		client, err := runlogclient.New(runlogclient.Options{
			Client:     mongoClient,
			Database:   database,
			Collection: collection,
			Timeout:    10 * time.Second,
		})
		require.NoError(t, err)

		const writers = 12
		start := make(chan struct{})
		results := make(chan runlog.AppendResult, writers)
		errs := make(chan error, writers)
		var writersDone sync.WaitGroup
		for range writers {
			writersDone.Add(1)
			go func() {
				defer writersDone.Done()
				<-start
				event := &runlog.Event{
					EventKey:  "concurrent-event",
					RunID:     "run-upgrade",
					AgentID:   "assistant",
					SessionID: integrationSessionID,
					TurnID:    "turn-concurrent",
					Type:      hooks.RunCompleted,
					Payload:   []byte(`{"ok":true}`),
					Timestamp: time.Unix(2, 0).UTC(),
				}
				result, appendErr := client.Append(ctx, event)
				results <- result
				errs <- appendErr
			}()
		}
		close(start)
		writersDone.Wait()
		close(results)
		close(errs)
		for appendErr := range errs {
			require.NoError(t, appendErr)
		}
		inserted := 0
		var eventID string
		for result := range results {
			if result.Inserted {
				inserted++
			}
			if eventID == "" {
				eventID = result.ID
			}
			require.Equal(t, eventID, result.ID)
		}
		require.Equal(t, 1, inserted)
		count, err := mongoClient.Database(database).Collection(collection).CountDocuments(ctx, bson.M{"event_key": "concurrent-event"})
		require.NoError(t, err)
		require.Equal(t, int64(1), count)

		conflictCollection := "runlog_index_conflict"
		_, err = mongoClient.Database(database).Collection(conflictCollection).InsertMany(ctx, []any{
			bson.M{"run_id": "run-conflict", "event_key": "duplicate"},
			bson.M{"run_id": "run-conflict", "event_key": "duplicate"},
		})
		require.NoError(t, err)
		_, err = runlogclient.New(runlogclient.Options{
			Client:     mongoClient,
			Database:   database,
			Collection: conflictCollection,
			Timeout:    10 * time.Second,
		})
		require.Error(t, err)
	})

	t.Run("session terminal state race", func(t *testing.T) {
		sessionsCollection := "session_upgrade"
		runsCollection := "session_upgrade_runs"
		startedAt := time.Unix(1, 0).UTC()
		_, err := mongoClient.Database(database).Collection(sessionsCollection).InsertOne(ctx, bson.M{
			"session_id": integrationSessionID,
			"status":     session.StatusActive,
			"created_at": startedAt,
			"updated_at": startedAt,
		})
		require.NoError(t, err)
		_, err = mongoClient.Database(database).Collection(runsCollection).InsertOne(ctx, bson.M{
			"run_id":     "run-terminal-race",
			"agent_id":   "assistant",
			"session_id": integrationSessionID,
			"status":     session.RunStatusRunning,
			"started_at": startedAt,
			"updated_at": startedAt,
		})
		require.NoError(t, err)
		client, err := sessionclient.New(sessionclient.Options{
			Client:             mongoClient,
			Database:           database,
			SessionsCollection: sessionsCollection,
			RunsCollection:     runsCollection,
			Timeout:            10 * time.Second,
		})
		require.NoError(t, err)

		require.NoError(t, client.UpsertRun(ctx, session.RunMeta{
			RunID:     "run-terminal-race",
			AgentID:   "assistant",
			SessionID: integrationSessionID,
			Status:    session.RunStatusCompleted,
		}))
		lateStatuses := []session.RunStatus{
			session.RunStatusFailed,
			session.RunStatusCanceled,
			session.RunStatusRunning,
		}
		start := make(chan struct{})
		errs := make(chan error, len(lateStatuses))
		var writersDone sync.WaitGroup
		for _, status := range lateStatuses {
			writersDone.Add(1)
			go func() {
				defer writersDone.Done()
				<-start
				errs <- client.UpsertRun(ctx, session.RunMeta{
					RunID:     "run-terminal-race",
					AgentID:   "assistant",
					SessionID: integrationSessionID,
					Status:    status,
				})
			}()
		}
		close(start)
		writersDone.Wait()
		close(errs)
		for updateErr := range errs {
			require.NoError(t, updateErr)
		}

		stored, err := client.LoadRun(ctx, "run-terminal-race")
		require.NoError(t, err)
		require.Equal(t, session.RunStatusCompleted, stored.Status)
		require.NoError(t, client.UpsertRun(ctx, session.RunMeta{
			RunID:     stored.RunID,
			AgentID:   stored.AgentID,
			SessionID: stored.SessionID,
			Status:    session.RunStatusRunning,
			Metadata:  map[string]any{"late": true},
		}))
		stored, err = client.LoadRun(ctx, stored.RunID)
		require.NoError(t, err)
		require.Equal(t, session.RunStatusCompleted, stored.Status)
		require.Equal(t, true, stored.Metadata["late"])
	})
}

func mustObjectID(t *testing.T, value string) bson.ObjectID {
	t.Helper()
	id, err := bson.ObjectIDFromHex(value)
	require.NoError(t, err)
	return id
}

func newMongoIntegrationClient(t *testing.T) (*mongodriver.Client, string) {
	t.Helper()
	if !dockerIntegrationRequested() {
		t.Skipf("set %s=1 to run Docker-backed Mongo contracts", runDockerIntegrationEnv)
	}
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
		if os.Getenv(requireDockerIntegrationEnv) == "1" {
			require.NoError(t, err, "Mongo testcontainer is required")
		}
		t.Skipf("Mongo testcontainer unavailable: %v", err)
	}
	t.Cleanup(func() {
		require.NoError(t, container.Terminate(context.Background()))
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
		require.NoError(t, client.Disconnect(context.Background()))
	})
	require.NoError(t, client.Ping(ctx, nil))

	database := "loom_mcp_" + sanitizeDatabaseName(t.Name())
	t.Cleanup(func() {
		require.NoError(t, client.Database(database).Drop(context.Background()))
	})
	return client, database
}

func dockerIntegrationRequested() bool {
	return os.Getenv(runDockerIntegrationEnv) == "1" || os.Getenv(requireDockerIntegrationEnv) == "1"
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
		body, readErr := io.ReadAll(output)
		require.NoError(t, readErr)
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
		body, readErr := io.ReadAll(output)
		require.NoError(t, readErr)
		t.Fatalf("Mongo replica set did not become primary: exit %d: %s", exitCode, body)
	}
}

func sanitizeDatabaseName(name string) string {
	replacer := strings.NewReplacer("/", "_", "-", "_", " ", "_")
	return replacer.Replace(name)
}
