package mongo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
)

func TestValidateAppendEventRejectsEveryRequiredField(t *testing.T) {
	valid := runlog.Event{RunID: testRunID, EventKey: testEventKey, Type: hooks.RunStarted, Timestamp: time.Unix(1, 0)}
	cases := []struct {
		name    string
		event   *runlog.Event
		message string
	}{
		{name: "nil", message: "event is required"},
		{name: "run_id", event: &runlog.Event{EventKey: valid.EventKey, Type: valid.Type, Timestamp: valid.Timestamp}, message: "run id is required"},
		{name: "event_key", event: &runlog.Event{RunID: valid.RunID, Type: valid.Type, Timestamp: valid.Timestamp}, message: "event key is required"},
		{name: "type", event: &runlog.Event{RunID: valid.RunID, EventKey: valid.EventKey, Timestamp: valid.Timestamp}, message: "event type is required"},
		{name: "timestamp", event: &runlog.Event{RunID: valid.RunID, EventKey: valid.EventKey, Type: valid.Type}, message: "timestamp is required"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			require.EqualError(t, validateAppendEvent(tt.event), tt.message)
		})
	}
	require.NoError(t, validateAppendEvent(&valid))
}

func TestRunlogEventDocumentDetachesPayloadAndNormalizesTimestamp(t *testing.T) {
	payload := []byte(`{"ok":true}`)
	timestamp := time.Date(2026, time.July, 12, 1, 2, 3, 456789123, time.FixedZone("offset", 3600))
	event := &runlog.Event{
		EventKey: testEventKey, RunID: testRunID, AgentID: testAgentID, SessionID: testSessionID,
		TurnID: testTurnID, Type: hooks.RunStarted, Payload: payload, Timestamp: timestamp,
	}

	doc := runlogEventDocument(event)
	payload[0] = '['

	assert.JSONEq(t, `{"ok":true}`, string(doc.Payload))
	assert.Equal(t, timestamp.UTC().Truncate(time.Millisecond), doc.Timestamp)
	assert.Equal(t, event.EventKey, doc.EventKey)
}

func TestEnsureIndexesCreatesCursorAndUniqueIdentityContracts(t *testing.T) {
	view := &captureIndexView{}
	coll := &fakeCollection{indexView: view}

	require.NoError(t, ensureIndexes(context.Background(), coll))
	require.Len(t, view.models, 2)
	assert.Equal(t, bson.D{{Key: fieldRunID, Value: 1}, {Key: fieldID, Value: 1}}, view.models[0].Keys)
	assert.Equal(t, bson.D{{Key: fieldRunID, Value: 1}, {Key: fieldEventKey, Value: 1}}, view.models[1].Keys)
	require.NotNil(t, view.models[1].Options)
	indexOpts, err := applyTestOptions[options.IndexOptions](view.models[1].Options)
	require.NoError(t, err)
	require.NotNil(t, indexOpts.Unique)
	assert.True(t, *indexOpts.Unique)

	wantErr := errors.New("create index")
	view.models = nil
	view.errAt = 1
	view.err = wantErr
	err = ensureIndexes(context.Background(), coll)
	require.ErrorIs(t, err, wantErr)
}

func TestNewClientWithCollectionAndListFilterContracts(t *testing.T) {
	_, err := newClientWithCollection(nil, nil, 0)
	require.ErrorContains(t, err, "collection is required")
	c, err := newClientWithCollection(nil, &fakeCollection{}, 0)
	require.NoError(t, err)
	assert.Equal(t, clientName, c.Name())
	assert.Equal(t, defaultTimeout, c.timeout)

	_, err = listRunlogFilter("", "", 1)
	require.EqualError(t, err, "run id is required")
	_, err = listRunlogFilter(testRunID, "", 0)
	require.EqualError(t, err, "limit must be > 0")
	_, err = listRunlogFilter(testRunID, "invalid", 1)
	require.ErrorContains(t, err, `invalid cursor "invalid"`)
	oid := mustOID(t)
	filter, err := listRunlogFilter(testRunID, oid.Hex(), 1)
	require.NoError(t, err)
	assert.Equal(t, bson.M{fieldRunID: testRunID, fieldID: bson.M{"$gt": oid}}, filter)
}

func TestAppendRejectsConflictingDuplicateAndUnexpectedInsertedID(t *testing.T) {
	duplicateErr := mongo.WriteException{WriteErrors: []mongo.WriteError{{Code: 11000, Message: "duplicate key"}}}
	existingID := mustOID(t)
	event := &runlog.Event{
		EventKey: testEventKey, RunID: testRunID, Type: hooks.RunStarted,
		Payload: []byte(`{"new":true}`), Timestamp: time.Unix(1, 0).UTC(),
	}
	coll := &fakeCollection{
		insertErr: duplicateErr,
		findOneDoc: eventDocument{
			ID: existingID, EventKey: testEventKey, RunID: testRunID, Type: string(hooks.RunStarted),
			Payload: []byte(`{"existing":true}`), Timestamp: time.Unix(1, 0).UTC(),
		},
	}
	c := &client{coll: coll}

	_, err := c.Append(context.Background(), event)
	require.ErrorContains(t, err, `event key "evt-1" conflicts with existing event body`)
	assert.Empty(t, event.ID)

	coll.insertErr = nil
	coll.insertedID = "not-an-object-id"
	_, err = c.Append(context.Background(), event)
	require.EqualError(t, err, "unexpected inserted id type string")
}

func TestAppendAndListHonorCanceledContexts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	coll := &contextCheckingCollection{fakeCollection: fakeCollection{insertedID: mustOID(t)}}
	c := &client{coll: coll, timeout: time.Second}
	event := &runlog.Event{EventKey: testEventKey, RunID: testRunID, Type: hooks.RunStarted, Timestamp: time.Unix(1, 0)}

	_, err := c.Append(ctx, event)
	require.ErrorIs(t, err, context.Canceled)
	_, err = c.List(ctx, testRunID, "", 1)
	require.ErrorIs(t, err, context.Canceled)
}

type captureIndexView struct {
	models []mongo.IndexModel
	errAt  int
	err    error
}

func (v *captureIndexView) CreateOne(_ context.Context, model mongo.IndexModel, _ ...options.Lister[options.CreateIndexesOptions]) (string, error) {
	v.models = append(v.models, model)
	if v.err != nil && len(v.models) == v.errAt {
		return "", v.err
	}
	return "index", nil
}

type contextCheckingCollection struct {
	fakeCollection
}

func (c *contextCheckingCollection) InsertOne(ctx context.Context, _ any, _ ...options.Lister[options.InsertOneOptions]) (*mongo.InsertOneResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &mongo.InsertOneResult{InsertedID: c.insertedID}, nil
}

func (c *contextCheckingCollection) Find(ctx context.Context, _ any, _ ...options.Lister[options.FindOptions]) (cursor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &fakeCursor{}, nil
}
