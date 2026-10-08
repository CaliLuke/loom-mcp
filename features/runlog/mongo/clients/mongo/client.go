// Package mongo implements the low-level MongoDB client used by the run log store.
package mongo

//go:generate cmg gen .

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"github.com/CaliLuke/loom/clue/health"

	clientinfra "github.com/CaliLuke/loom-mcp/v2/features/mongo/clientinfra"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
)

type (
	// Client exposes Mongo-backed operations for the run event log.
	Client interface {
		health.Pinger

		Append(ctx context.Context, e *runlog.Event) (runlog.AppendResult, error)
		List(ctx context.Context, runID string, cursor string, limit int) (runlog.Page, error)
	}

	// Options configures the Mongo client implementation.
	Options struct {
		Client     *mongodriver.Client
		Database   string
		Collection string
		Timeout    time.Duration
	}

	client struct {
		mongo   *mongodriver.Client
		coll    collection
		timeout time.Duration
	}

	eventDocument struct {
		ID        bson.ObjectID `bson:"_id,omitempty"`
		EventKey  string        `bson:"event_key"`
		RunID     string        `bson:"run_id"`
		AgentID   string        `bson:"agent_id"`
		SessionID string        `bson:"session_id"`
		TurnID    string        `bson:"turn_id"`
		Type      string        `bson:"type"`
		Payload   []byte        `bson:"payload"`
		Timestamp time.Time     `bson:"timestamp"`
		Sequence  int64         `bson:"sequence,omitempty"`
	}
)

const (
	defaultCollection = "agent_run_events"
	defaultTimeout    = 5 * time.Second
	clientName        = "runlog-mongo"
	fieldEventKey     = "event_key"
	fieldID           = "_id"
	fieldRunID        = "run_id"
	fieldSequence     = "sequence"
	operatorExists    = "$exists"
)

// New returns a Client backed by the provided MongoDB client.
func New(opts Options) (Client, error) {
	if err := clientinfra.ValidateMongoOptions(opts.Client, opts.Database); err != nil {
		return nil, err
	}
	collection := clientinfra.ResolveCollectionName(opts.Collection, defaultCollection)
	timeout := clientinfra.ResolveTimeout(opts.Timeout, defaultTimeout)

	wrapper := clientinfra.NewCollection(opts.Client, opts.Database, collection)
	wrapper = clientinfra.Collection{Coll: wrapper.Coll.Clone(options.Collection().SetReadPreference(readpref.Primary()))}
	if err := clientinfra.EnsureIndexes(timeout, func(ctx context.Context) error {
		return ensureIndexes(ctx, wrapper)
	}); err != nil {
		return nil, err
	}
	migrationCtx, cancel := clientinfra.WithTimeout(context.Background(), timeout, false)
	defer cancel()
	if err := migrateLegacySequences(migrationCtx, wrapper); err != nil {
		return nil, err
	}
	return newClientWithCollection(opts.Client, wrapper, timeout)
}

func (c *client) Name() string {
	return clientName
}

func (c *client) Ping(ctx context.Context) error {
	return clientinfra.Ping(ctx, c.mongo, false)
}

func (c *client) Append(ctx context.Context, e *runlog.Event) (runlog.AppendResult, error) {
	if err := validateAppendEvent(e); err != nil {
		return runlog.AppendResult{}, err
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	doc := runlogEventDocument(e)
	for {
		sequence, err := maxCommittedSequence(ctx, c.coll, e.RunID)
		if err != nil {
			return runlog.AppendResult{}, err
		}
		if sequence == math.MaxInt64 {
			existing, lookupErr := c.lookupEventByKey(ctx, e.RunID, e.EventKey)
			if lookupErr == nil {
				return duplicateAppendResult(e, existing, doc)
			}
			if !errors.Is(lookupErr, mongodriver.ErrNoDocuments) {
				return runlog.AppendResult{}, lookupErr
			}
			return runlog.AppendResult{}, errors.New("runlog sequence overflow")
		}
		doc.Sequence = sequence + 1
		res, insertErr := c.coll.InsertOne(ctx, doc)
		if insertErr == nil {
			return assignInsertedEventID(e, res.InsertedID)
		}
		if !mongodriver.IsDuplicateKeyError(insertErr) {
			return runlog.AppendResult{}, insertErr
		}
		result, resolveErr := c.resolveDuplicateAppend(ctx, e, doc, insertErr)
		if resolveErr == nil {
			return result, nil
		}
		if !errors.Is(resolveErr, errSequenceCollision) {
			return runlog.AppendResult{}, resolveErr
		}
	}
}

func (c *client) List(ctx context.Context, runID string, cursor string, limit int) (page runlog.Page, err error) {
	filter, err := listRunlogFilter(runID, limit)
	if err != nil {
		return runlog.Page{}, err
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	after, err := c.cursorSequence(ctx, runID, cursor)
	if err != nil {
		return runlog.Page{}, err
	}
	filter[fieldSequence] = bson.M{"$gt": after}

	cur, err := c.coll.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: fieldSequence, Value: 1}}).
		SetLimit(int64(limit+1)),
	)
	if err != nil {
		return runlog.Page{}, err
	}
	defer func() {
		if cerr := cur.Close(ctx); err == nil && cerr != nil {
			err = cerr
		}
	}()

	events, err := decodeRunlogEvents(ctx, cur)
	if err != nil {
		return runlog.Page{}, err
	}
	if err := cur.Err(); err != nil {
		return runlog.Page{}, err
	}
	return buildRunlogPage(events, limit), nil
}

// cursorSequence resolves an ObjectID cursor to the event's durable ordering value.
func (c *client) cursorSequence(ctx context.Context, runID, cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	oid, err := bson.ObjectIDFromHex(cursor)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor %q: %w", cursor, err)
	}
	var cursorDoc eventDocument
	if err := c.coll.FindOne(ctx, bson.M{fieldRunID: runID, fieldID: oid}).Decode(&cursorDoc); err != nil {
		if errors.Is(err, mongodriver.ErrNoDocuments) {
			return 0, fmt.Errorf("cursor %q does not identify an event in run %q", cursor, runID)
		}
		return 0, err
	}
	return cursorDoc.Sequence, nil
}

func validateAppendEvent(e *runlog.Event) error {
	switch {
	case e == nil:
		return errors.New("event is required")
	case e.RunID == "":
		return errors.New("run id is required")
	case e.EventKey == "":
		return errors.New("event key is required")
	case e.Type == "":
		return errors.New("event type is required")
	case e.Timestamp.IsZero():
		return errors.New("timestamp is required")
	default:
		return nil
	}
}

func runlogEventDocument(e *runlog.Event) eventDocument {
	return eventDocument{
		EventKey:  e.EventKey,
		RunID:     e.RunID,
		AgentID:   string(e.AgentID),
		SessionID: e.SessionID,
		TurnID:    e.TurnID,
		Type:      string(e.Type),
		Payload:   append([]byte(nil), e.Payload...),
		Timestamp: mongoTimestamp(e.Timestamp),
	}
}

// resolveDuplicateAppend separates an event-key replay from a confirmed sequence race.
func (c *client) resolveDuplicateAppend(ctx context.Context, e *runlog.Event, doc eventDocument, insertErr error) (runlog.AppendResult, error) {
	existing, lookupErr := c.lookupEventByKey(ctx, e.RunID, e.EventKey)
	if lookupErr != nil {
		if !errors.Is(lookupErr, mongodriver.ErrNoDocuments) {
			return runlog.AppendResult{}, lookupErr
		}
		collision, collisionErr := sequenceCollision(ctx, c.coll, e.RunID, doc.Sequence)
		if collisionErr != nil {
			return runlog.AppendResult{}, collisionErr
		}
		if collision {
			return runlog.AppendResult{}, errSequenceCollision
		}
		return runlog.AppendResult{}, insertErr
	}
	return duplicateAppendResult(e, existing, doc)
}

// duplicateAppendResult accepts an exact event-key replay and rejects a conflicting body.
func duplicateAppendResult(e *runlog.Event, existing eventDocument, doc eventDocument) (runlog.AppendResult, error) {
	if !sameEventDocument(existing, doc) {
		return runlog.AppendResult{}, fmt.Errorf("event key %q conflicts with existing event body", e.EventKey)
	}
	e.ID = existing.ID.Hex()
	return runlog.AppendResult{ID: e.ID, Inserted: false}, nil
}

func assignInsertedEventID(e *runlog.Event, insertedID any) (runlog.AppendResult, error) {
	oid, ok := insertedID.(bson.ObjectID)
	if !ok {
		return runlog.AppendResult{}, fmt.Errorf("unexpected inserted id type %T", insertedID)
	}
	e.ID = oid.Hex()
	return runlog.AppendResult{ID: e.ID, Inserted: true}, nil
}

func listRunlogFilter(runID string, limit int) (bson.M, error) {
	if runID == "" {
		return nil, errors.New("run id is required")
	}
	if limit <= 0 {
		return nil, errors.New("limit must be > 0")
	}
	filter := bson.M{fieldRunID: runID}
	return filter, nil
}

func decodeRunlogEvents(ctx context.Context, cur cursor) ([]*runlog.Event, error) {
	var events []*runlog.Event
	for cur.Next(ctx) {
		var doc eventDocument
		if err := cur.Decode(&doc); err != nil {
			return nil, err
		}
		events = append(events, runlogEventFromDocument(doc))
	}
	return events, nil
}

func runlogEventFromDocument(doc eventDocument) *runlog.Event {
	return &runlog.Event{
		ID:        doc.ID.Hex(),
		EventKey:  doc.EventKey,
		RunID:     doc.RunID,
		AgentID:   agent.Ident(doc.AgentID),
		SessionID: doc.SessionID,
		TurnID:    doc.TurnID,
		Type:      hooks.EventType(doc.Type),
		Payload:   append([]byte(nil), doc.Payload...),
		Timestamp: doc.Timestamp,
	}
}

func buildRunlogPage(events []*runlog.Event, limit int) runlog.Page {
	var next string
	if len(events) > limit {
		next = events[limit-1].ID
		events = events[:limit]
	}
	return runlog.Page{Events: events, NextCursor: next}
}

func (c *client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return clientinfra.WithTimeout(ctx, c.timeout, false)
}

func ensureIndexes(ctx context.Context, coll collection) error {
	cursorIndex := mongodriver.IndexModel{
		Keys: bson.D{
			{Key: fieldRunID, Value: 1},
			{Key: fieldID, Value: 1},
		},
	}
	if _, err := coll.Indexes().CreateOne(ctx, cursorIndex); err != nil {
		return err
	}
	identityIndex := mongodriver.IndexModel{
		Keys: bson.D{
			{Key: fieldRunID, Value: 1},
			{Key: fieldEventKey, Value: 1},
		},
		Options: options.Index().SetUnique(true),
	}
	if _, err := coll.Indexes().CreateOne(ctx, identityIndex); err != nil {
		return err
	}
	sequenceIndex := mongodriver.IndexModel{
		Keys: bson.D{
			{Key: fieldRunID, Value: 1},
			{Key: fieldSequence, Value: 1},
		},
		Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{
			fieldSequence: bson.M{operatorExists: true},
		}),
	}
	_, err := coll.Indexes().CreateOne(ctx, sequenceIndex)
	return err
}

func newClientWithCollection(mongoClient *mongodriver.Client, coll collection, timeout time.Duration) (*client, error) {
	if err := clientinfra.ValidateCollections("collection is required", coll); err != nil {
		return nil, err
	}
	timeout = clientinfra.ResolveTimeout(timeout, defaultTimeout)
	return &client{
		mongo:   mongoClient,
		coll:    coll,
		timeout: timeout,
	}, nil
}

type collection interface {
	clientinfra.InsertOneCollection
	clientinfra.FindOneCollection
	clientinfra.FindCollection
	clientinfra.UpdateOneCollection
	clientinfra.IndexedCollection
}

type singleResult = clientinfra.SingleResultDecoder

type cursor = clientinfra.CursorReader

type indexView = clientinfra.IndexCreator

func (c *client) lookupEventByKey(ctx context.Context, runID string, eventKey string) (eventDocument, error) {
	var doc eventDocument
	err := c.coll.FindOne(ctx, bson.M{
		fieldRunID:    runID,
		fieldEventKey: eventKey,
	}).Decode(&doc)
	if err != nil {
		return eventDocument{}, err
	}
	return doc, nil
}

func sameEventDocument(existing eventDocument, candidate eventDocument) bool {
	return existing.EventKey == candidate.EventKey &&
		existing.RunID == candidate.RunID &&
		existing.AgentID == candidate.AgentID &&
		existing.SessionID == candidate.SessionID &&
		existing.TurnID == candidate.TurnID &&
		existing.Type == candidate.Type &&
		existing.Timestamp.Equal(candidate.Timestamp) &&
		bytes.Equal(existing.Payload, candidate.Payload)
}

func mongoTimestamp(t time.Time) time.Time {
	return t.UTC().Truncate(time.Millisecond)
}
