package mongo

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
)

func TestClientListDoesNotSkipLaterLowerObjectID(t *testing.T) {
	ts := time.Unix(1_791_374_400, 0).UTC()
	id1 := bson.NewObjectIDFromTimestamp(ts)
	id2 := bson.NewObjectIDFromTimestamp(ts)
	id3 := bson.NewObjectIDFromTimestamp(ts)
	id1[4], id2[4], id3[4] = 0x80, 0x80, 0x10
	id1[9], id1[10], id1[11] = 0, 0, 1
	id2[9], id2[10], id2[11] = 0, 0, 2
	id3[9], id3[10], id3[11] = 0, 0, 1

	coll := &fakeCollection{findDocs: []eventDocument{
		{ID: id1, Sequence: 1, RunID: testRunID, EventKey: "first", Type: string(hooks.RunStarted), Timestamp: ts},
		{ID: id2, Sequence: 2, RunID: testRunID, EventKey: "second", Type: string(hooks.RunStarted), Timestamp: ts.Add(time.Millisecond)},
	}}
	c := &client{coll: coll}

	page, err := c.List(context.Background(), testRunID, "", 1)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, "first", page.Events[0].EventKey)
	require.Equal(t, id1.Hex(), page.NextCursor)

	coll.findDocs = append(coll.findDocs, eventDocument{
		ID: id3, Sequence: 3, RunID: testRunID, EventKey: "later", Type: string(hooks.RunStarted), Timestamp: ts.Add(2 * time.Millisecond),
	})
	next, err := c.List(context.Background(), testRunID, page.NextCursor, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"second", "later"}, eventKeys(next.Events))
}

func TestClientAppendRetriesOnlyConfirmedSequenceCollision(t *testing.T) {
	sequenceErr := mongodriver.WriteException{WriteErrors: []mongodriver.WriteError{{Code: 11000, Message: "duplicate sequence"}}}
	coll := &fakeCollection{insertedID: mustOID(t)}
	coll.beforeInsert = func(c *fakeCollection, doc eventDocument) error {
		if len(c.findDocs) != 0 {
			return nil
		}
		winner := doc
		winner.ID = bson.NewObjectID()
		winner.EventKey = "winner"
		c.findDocs = append(c.findDocs, winner)
		return sequenceErr
	}
	c := &client{coll: coll}
	event := validAppendEvent("later-event")

	result, err := c.Append(context.Background(), event)
	require.NoError(t, err)
	require.True(t, result.Inserted)
	require.Len(t, coll.findDocs, 2)
	assert.EqualValues(t, 1, coll.findDocs[0].Sequence)
	assert.EqualValues(t, 2, coll.findDocs[1].Sequence)
	assert.Equal(t, "later-event", coll.findDocs[1].EventKey)
}

func TestClientAppendPreservesUnconfirmedDuplicateError(t *testing.T) {
	duplicateErr := mongodriver.WriteException{WriteErrors: []mongodriver.WriteError{{Code: 11000, Message: "other unique index"}}}
	coll := &fakeCollection{insertErr: duplicateErr}
	c := &client{coll: coll}
	_, err := c.Append(context.Background(), validAppendEvent(testEventKey))
	require.EqualError(t, err, duplicateErr.Error())
}

func TestClientAppendRejectsSequenceOverflow(t *testing.T) {
	coll := &fakeCollection{findDocs: []eventDocument{{RunID: testRunID, Sequence: math.MaxInt64}}}
	c := &client{coll: coll}
	_, err := c.Append(context.Background(), validAppendEvent(testEventKey))
	require.EqualError(t, err, "runlog sequence overflow")
}

func TestClientAppendReplaySucceedsAtSequenceOverflow(t *testing.T) {
	event := validAppendEvent(testEventKey)
	doc := runlogEventDocument(event)
	doc.ID = mustOID(t)
	doc.Sequence = math.MaxInt64
	coll := &fakeCollection{findDocs: []eventDocument{doc}}
	c := &client{coll: coll}

	result, err := c.Append(context.Background(), event)
	require.NoError(t, err)
	assert.False(t, result.Inserted)
	assert.Equal(t, doc.ID.Hex(), result.ID)
}

func TestClientListRejectsMalformedAndUnknownCursors(t *testing.T) {
	c := &client{coll: &fakeCollection{findDocs: fakeEventDocuments(testRunID, 1)}}
	_, err := c.List(context.Background(), testRunID, "invalid", 1)
	require.ErrorContains(t, err, `invalid cursor "invalid"`)
	unknown := bson.ObjectID{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	_, err = c.List(context.Background(), testRunID, unknown.Hex(), 1)
	require.ErrorContains(t, err, "does not identify an event")
	_, err = c.List(context.Background(), "another-run", fakeEventDocuments(testRunID, 1)[0].ID.Hex(), 1)
	require.ErrorContains(t, err, "does not identify an event")
}

func TestMigrateLegacySequencesPreservesObjectIDOrderAndResumes(t *testing.T) {
	second := bson.ObjectID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
	first := bson.ObjectID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	coll := &fakeCollection{findDocs: []eventDocument{
		{ID: second, RunID: testRunID, EventKey: "second"},
		{ID: first, RunID: testRunID, EventKey: "first"},
	}}

	require.NoError(t, migrateLegacySequences(context.Background(), coll))
	assert.EqualValues(t, 1, findDocumentByID(t, coll.findDocs, first).Sequence)
	assert.EqualValues(t, 2, findDocumentByID(t, coll.findDocs, second).Sequence)
	require.NoError(t, migrateLegacySequences(context.Background(), coll))
	assert.EqualValues(t, 1, findDocumentByID(t, coll.findDocs, first).Sequence)
	assert.EqualValues(t, 2, findDocumentByID(t, coll.findDocs, second).Sequence)
}

func TestMigrateLegacySequencesResumesPartialMigrationWithoutChangingStoredEvent(t *testing.T) {
	first := bson.ObjectID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	second := bson.ObjectID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
	payload := []byte(`{"legacy":true}`)
	coll := &fakeCollection{findDocs: []eventDocument{
		{ID: first, Sequence: 1, RunID: testRunID, EventKey: "first", Payload: payload, Timestamp: time.Unix(1, 0).UTC()},
		{ID: second, RunID: testRunID, EventKey: "second", Payload: []byte(`{"next":true}`), Timestamp: time.Unix(2, 0).UTC()},
	}}

	require.NoError(t, migrateLegacySequences(context.Background(), coll))
	firstAfter := findDocumentByID(t, coll.findDocs, first)
	secondAfter := findDocumentByID(t, coll.findDocs, second)
	assert.EqualValues(t, 1, firstAfter.Sequence)
	assert.Equal(t, "first", firstAfter.EventKey)
	assert.Equal(t, payload, firstAfter.Payload)
	assert.Equal(t, time.Unix(1, 0).UTC(), firstAfter.Timestamp)
	assert.EqualValues(t, 2, secondAfter.Sequence)
	assert.Equal(t, "second", secondAfter.EventKey)
	assert.Equal(t, []byte(`{"next":true}`), secondAfter.Payload)
}

func TestMigrateLegacySequencesReturnsUpdateFailures(t *testing.T) {
	wantErr := errors.New("migration update failed")
	coll := &fakeCollection{
		findDocs:  []eventDocument{{ID: mustOID(t), RunID: testRunID}},
		updateErr: wantErr,
	}
	require.ErrorIs(t, migrateLegacySequences(context.Background(), coll), wantErr)
}

func TestMigrateLegacySequencesHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, migrateLegacySequences(ctx, &fakeCollection{}), context.Canceled)
}

func eventKeys(events []*runlog.Event) []string {
	keys := make([]string, len(events))
	for i, event := range events {
		keys[i] = event.EventKey
	}
	return keys
}

func validAppendEvent(eventKey string) *runlog.Event {
	return &runlog.Event{
		EventKey: eventKey, RunID: testRunID, AgentID: testAgentID, SessionID: testSessionID,
		TurnID: testTurnID, Type: hooks.RunStarted, Payload: []byte(`{"ok":true}`), Timestamp: time.Unix(1, 0).UTC(),
	}
}

func findDocumentByID(t *testing.T, docs []eventDocument, id bson.ObjectID) eventDocument {
	t.Helper()
	for _, doc := range docs {
		if doc.ID == id {
			return doc
		}
	}
	t.Fatalf("document %s not found", id.Hex())
	return eventDocument{}
}
