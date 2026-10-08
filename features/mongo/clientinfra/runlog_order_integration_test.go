package clientinfra_test

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	runlogclient "github.com/CaliLuke/loom-mcp/v2/features/runlog/mongo/clients/mongo"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
)

func TestMongoDriverV2RunlogOrderedCursors(t *testing.T) {
	for _, deployment := range []struct {
		name string
		kind mongoDeployment
	}{
		{name: "replica_set", kind: mongoReplicaSet},
		{name: "standalone", kind: mongoStandalone},
	} {
		t.Run(deployment.name, func(t *testing.T) {
			mongoClient, database, address := newMongoIntegrationClientFor(t, deployment.kind)
			t.Run("lower_object_id_after_cursor", func(t *testing.T) {
				testRunlogLowerObjectIDAfterCursor(t, mongoClient, database)
			})
			t.Run("concurrent_writers", func(t *testing.T) {
				testRunlogConcurrentWriters(t, mongoClient, database)
			})
			t.Run("concurrent_legacy_migration", func(t *testing.T) {
				testRunlogConcurrentMigration(t, mongoClient, database)
			})
			t.Run("compact_bson_integers", func(t *testing.T) {
				testRunlogCompactIntegers(t, address, database)
			})
		})
	}
}

func testRunlogLowerObjectIDAfterCursor(t *testing.T, mongoClient *mongodriver.Client, database string) {
	t.Helper()
	ctx := t.Context()
	const collectionName = "cursor_order"
	coll := mongoClient.Database(database).Collection(collectionName)
	// Foreign legacy IDs sort above IDs minted by the current process. Event
	// timestamps deliberately do not establish append order either.
	firstID, err := bson.ObjectIDFromHex("ffffffff8000000000000001")
	require.NoError(t, err)
	secondID := firstID
	secondID[11] = 2
	_, err = coll.InsertMany(ctx, []any{
		legacyRunlogDocument(secondID, "second"),
		legacyRunlogDocument(firstID, "first"),
	})
	require.NoError(t, err)
	client, err := runlogclient.New(runlogclient.Options{Client: mongoClient, Database: database, Collection: collectionName, Timeout: 10 * time.Second})
	require.NoError(t, err)
	page, err := client.List(ctx, "ordered", "", 1)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	assert.Equal(t, firstID.Hex(), page.NextCursor)
	assert.Equal(t, "first", page.Events[0].EventKey)

	later := newOrderedRunlogEvent("later")
	result, err := client.Append(ctx, later)
	require.NoError(t, err)
	laterID, err := bson.ObjectIDFromHex(result.ID)
	require.NoError(t, err)
	require.Negative(t, bytes.Compare(laterID[:], firstID[:]), "exercise lower ObjectID after an already returned cursor")
	next, err := client.List(ctx, "ordered", page.NextCursor, 10)
	require.NoError(t, err)
	require.Len(t, next.Events, 2, "a later append must remain reachable regardless of ObjectID order")
	assert.Equal(t, []string{"second", "later"}, []string{next.Events[0].EventKey, next.Events[1].EventKey})
	assert.Equal(t, secondID.Hex(), next.Events[0].ID)
	assert.Equal(t, []byte(`{"legacy":true}`), []byte(next.Events[0].Payload))
	assertRunlogSequences(t, coll, 3)

	_, err = client.List(ctx, "different-run", page.NextCursor, 1)
	require.Error(t, err, "a cursor must belong to the requested run")
}

func testRunlogConcurrentWriters(t *testing.T, mongoClient *mongodriver.Client, database string) {
	t.Helper()
	const collectionName = "concurrent_order"
	clients := make([]runlogclient.Client, 2)
	for i := range clients {
		var err error
		clients[i], err = runlogclient.New(runlogclient.Options{Client: mongoClient, Database: database, Collection: collectionName, Timeout: 30 * time.Second})
		require.NoError(t, err)
	}
	const writers = 24
	start := make(chan struct{})
	errs := make(chan error, writers)
	var done sync.WaitGroup
	for i := range writers {
		done.Go(func() {
			<-start
			_, err := clients[i%len(clients)].Append(t.Context(), newOrderedRunlogEvent(fmt.Sprintf("event-%02d", i)))
			errs <- err
		})
	}
	close(start)
	done.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	seen := make(map[string]bool)
	cursor := ""
	for {
		page, err := clients[0].List(t.Context(), "ordered", cursor, 3)
		require.NoError(t, err)
		for _, event := range page.Events {
			assert.False(t, seen[event.EventKey], "event returned twice: %s", event.EventKey)
			seen[event.EventKey] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	assert.Len(t, seen, writers)
	assertRunlogSequences(t, mongoClient.Database(database).Collection(collectionName), writers)
}

func testRunlogConcurrentMigration(t *testing.T, mongoClient *mongodriver.Client, database string) {
	t.Helper()
	const collectionName = "concurrent_migration"
	coll := mongoClient.Database(database).Collection(collectionName)
	const count = 20
	docs := make([]any, 0, count)
	for i := count; i > 0; i-- {
		id := bson.ObjectID{}
		id[11] = byte(i)
		docs = append(docs, legacyRunlogDocument(id, fmt.Sprintf("legacy-%02d", i)))
	}
	_, err := coll.InsertMany(t.Context(), docs)
	require.NoError(t, err)
	const constructors = 6
	start := make(chan struct{})
	errs := make(chan error, constructors)
	var done sync.WaitGroup
	for range constructors {
		done.Go(func() {
			<-start
			_, err := runlogclient.New(runlogclient.Options{Client: mongoClient, Database: database, Collection: collectionName, Timeout: 30 * time.Second})
			errs <- err
		})
	}
	close(start)
	done.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	client, err := runlogclient.New(runlogclient.Options{Client: mongoClient, Database: database, Collection: collectionName, Timeout: 30 * time.Second})
	require.NoError(t, err)
	page, err := client.List(t.Context(), "ordered", "", count)
	require.NoError(t, err)
	require.Len(t, page.Events, count)
	for i, event := range page.Events {
		assert.Equal(t, fmt.Sprintf("legacy-%02d", i+1), event.EventKey)
	}
	assertRunlogSequences(t, coll, count)
}

func testRunlogCompactIntegers(t *testing.T, address, database string) {
	t.Helper()
	compactClient, err := mongodriver.Connect(options.Client().
		ApplyURI(fmt.Sprintf("mongodb://%s/?directConnection=true", address)).
		SetBSONOptions(&options.BSONOptions{IntMinSize: true}))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, compactClient.Disconnect(context.Background()))
	})
	const collectionName = "compact_sequences"
	client, err := runlogclient.New(runlogclient.Options{Client: compactClient, Database: database, Collection: collectionName, Timeout: 10 * time.Second})
	require.NoError(t, err)
	for i := range 3 {
		_, err := client.Append(t.Context(), newOrderedRunlogEvent(fmt.Sprintf("compact-%d", i)))
		require.NoError(t, err)
	}
	coll := compactClient.Database(database).Collection(collectionName)
	var raw bson.Raw
	require.NoError(t, coll.FindOne(t.Context(), bson.M{}).Decode(&raw))
	assert.Equal(t, bson.TypeInt32, raw.Lookup("sequence").Type)
	assertRunlogSequences(t, coll, 3)
	page, err := client.List(t.Context(), "ordered", "", 1)
	require.NoError(t, err)
	require.NotEmpty(t, page.NextCursor)
	remaining, err := client.List(t.Context(), "ordered", page.NextCursor, 10)
	require.NoError(t, err)
	require.Len(t, remaining.Events, 2)
	assert.Equal(t, "compact-1", remaining.Events[0].EventKey)
	assert.Equal(t, "compact-2", remaining.Events[1].EventKey)
}

// legacyRunlogDocument builds a pre-sequence record without depending on client internals.
func legacyRunlogDocument(id bson.ObjectID, key string) bson.M {
	return bson.M{
		"_id": id, "run_id": "ordered", "event_key": key,
		"type": string(hooks.RunStarted), "payload": []byte(`{"legacy":true}`),
		"timestamp": time.Unix(10, 0).UTC(),
	}
}

func newOrderedRunlogEvent(key string) *runlog.Event {
	return &runlog.Event{
		RunID: "ordered", EventKey: key, Type: hooks.RunStarted,
		Payload: []byte(`{"new":true}`), Timestamp: time.Unix(1, 0).UTC(),
	}
}

// assertRunlogSequences verifies real Mongo index allocation has no duplicate or uncommitted gaps.
func assertRunlogSequences(t *testing.T, coll *mongodriver.Collection, count int) {
	t.Helper()
	cursor, err := coll.Find(t.Context(), bson.M{"run_id": "ordered"}, options.Find().SetSort(bson.D{{Key: "sequence", Value: 1}}))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, cursor.Close(context.Background()))
	})
	var documents []struct {
		Sequence int64 `bson:"sequence"`
	}
	require.NoError(t, cursor.All(t.Context(), &documents))
	require.Len(t, documents, count)
	for i, doc := range documents {
		assert.Equal(t, int64(i+1), doc.Sequence)
	}
}
