// Package mongo implements the low-level MongoDB client used by the memory store.
package mongo

//go:generate cmg gen .

import (
	"context"
	"errors"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/CaliLuke/loom/clue/health"

	clientinfra "github.com/CaliLuke/loom-mcp/v2/features/mongo/clientinfra"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
)

const (
	defaultCollection       = "agent_memory"
	defaultEventsCollection = "agent_memory_events"
	defaultTimeout          = 5 * time.Second
	clientName              = "memory-mongo"
	fieldAgentID            = "agent_id"
	fieldCreatedAt          = "created_at"
	fieldID                 = "_id"
	fieldRunID              = "run_id"
)

// Client exposes Mongo-backed operations for memory snapshots.
type Client interface {
	health.Pinger

	LoadRun(ctx context.Context, agentID, runID string) (memory.Snapshot, error)
	AppendEvents(ctx context.Context, agentID, runID string, events []memory.Event) error
}

// Options configures the Mongo client implementation.
type Options struct {
	Client     *mongodriver.Client
	Database   string
	Collection string
	// EventsCollection stores immutable append buckets. When empty, it defaults
	// to Collection + "_events", or "agent_memory_events" when Collection is
	// also empty. Collection remains the read-only legacy snapshot source.
	EventsCollection string
	Timeout          time.Duration
}

type client struct {
	mongo      *mongodriver.Client
	legacyColl legacyCollection
	eventColl  eventCollection
	timeout    time.Duration
}

// New returns a Client backed by the provided MongoDB client.
func New(opts Options) (Client, error) {
	if err := clientinfra.ValidateMongoOptions(opts.Client, opts.Database); err != nil {
		return nil, err
	}
	collection := clientinfra.ResolveCollectionName(opts.Collection, defaultCollection)
	eventsCollection := resolveEventsCollection(opts.Collection, opts.EventsCollection)
	if eventsCollection == collection {
		return nil, errors.New("events collection must differ from legacy collection")
	}
	timeout := clientinfra.ResolveTimeout(opts.Timeout, defaultTimeout)
	legacyWrapper := clientinfra.NewCollection(opts.Client, opts.Database, collection)
	eventWrapper := clientinfra.NewCollection(opts.Client, opts.Database, eventsCollection)
	if err := clientinfra.EnsureIndexes(timeout, func(ctx context.Context) error {
		if err := ensureLegacyIndexes(ctx, legacyWrapper); err != nil {
			return err
		}
		return ensureEventIndexes(ctx, eventWrapper)
	}); err != nil {
		return nil, err
	}
	return newClientWithCollections(opts.Client, legacyWrapper, eventWrapper, timeout)
}

func resolveEventsCollection(legacyCollection, eventsCollection string) string {
	if eventsCollection != "" {
		return eventsCollection
	}
	if legacyCollection != "" {
		return legacyCollection + "_events"
	}
	return defaultEventsCollection
}

func (c *client) Name() string {
	return clientName
}

func (c *client) Ping(ctx context.Context) error {
	return clientinfra.Ping(ctx, c.mongo, true)
}

func (c *client) LoadRun(ctx context.Context, agentID, runID string) (memory.Snapshot, error) {
	if agentID == "" {
		return memory.Snapshot{}, errors.New("agent id is required")
	}
	if runID == "" {
		return memory.Snapshot{}, errors.New("run id is required")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	legacy, err := c.loadLegacyRun(ctx, agentID, runID)
	if err != nil {
		return memory.Snapshot{}, err
	}
	bucketEvents, err := c.loadEventBuckets(ctx, agentID, runID)
	if err != nil {
		return memory.Snapshot{}, err
	}
	events := append(fromEventDocuments(legacy.Events), bucketEvents...)
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Timestamp.Before(events[j].Timestamp)
	})
	meta := cloneMeta(legacy.Meta)
	if meta == nil {
		meta = make(map[string]any)
	}
	return memory.Snapshot{
		AgentID: agentID,
		RunID:   runID,
		Events:  events,
		Meta:    meta,
	}, nil
}

func (c *client) AppendEvents(ctx context.Context, agentID, runID string, events []memory.Event) error {
	if agentID == "" {
		return errors.New("agent id is required")
	}
	if runID == "" {
		return errors.New("run id is required")
	}
	if len(events) == 0 {
		return nil
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	now := time.Now().UTC()
	bucket := eventBucketDocument{
		AgentID:   agentID,
		RunID:     runID,
		Events:    toEventDocuments(events, now),
		CreatedAt: now,
	}
	_, err := c.eventColl.InsertOne(ctx, bucket)
	return err
}

func (c *client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return clientinfra.WithTimeout(ctx, c.timeout, true)
}

type runDocument struct {
	AgentID   string          `bson:"agent_id"`
	RunID     string          `bson:"run_id"`
	Events    []eventDocument `bson:"events"`
	Meta      map[string]any  `bson:"meta,omitempty"`
	UpdatedAt time.Time       `bson:"updated_at,omitempty"`
}

type eventBucketDocument struct {
	ID        bson.ObjectID   `bson:"_id,omitempty"`
	AgentID   string          `bson:"agent_id"`
	RunID     string          `bson:"run_id"`
	Events    []eventDocument `bson:"events"`
	CreatedAt time.Time       `bson:"created_at"`
}

type eventDocument struct {
	Type      memory.EventType  `bson:"type"`
	Timestamp time.Time         `bson:"timestamp"`
	Data      any               `bson:"data,omitempty"`
	Labels    map[string]string `bson:"labels,omitempty"`
}

func toEventDocuments(events []memory.Event, fallback time.Time) []eventDocument {
	result := make([]eventDocument, len(events))
	for i, evt := range events {
		ts := evt.Timestamp
		if ts.IsZero() {
			ts = fallback
		}
		result[i] = eventDocument{
			Type:      evt.Type,
			Timestamp: ts.UTC(),
			Data:      evt.Data,
			Labels:    cloneLabels(evt.Labels),
		}
	}
	return result
}

func fromEventDocuments(events []eventDocument) []memory.Event {
	if len(events) == 0 {
		return nil
	}
	result := make([]memory.Event, len(events))
	for i, evt := range events {
		result[i] = memory.Event{
			Type:      evt.Type,
			Timestamp: evt.Timestamp,
			Data:      clientinfra.NormalizeBSONValue(evt.Data),
			Labels:    cloneLabels(evt.Labels),
		}
	}
	return result
}

func cloneLabels(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneMeta(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = clientinfra.NormalizeBSONValue(v)
	}
	return dst
}

func ensureLegacyIndexes(ctx context.Context, coll legacyCollection) error {
	index := mongodriver.IndexModel{
		Keys:    bson.D{{Key: fieldAgentID, Value: 1}, {Key: fieldRunID, Value: 1}},
		Options: options.Index().SetUnique(true),
	}
	_, err := coll.Indexes().CreateOne(ctx, index)
	return err
}

func ensureEventIndexes(ctx context.Context, coll eventCollection) error {
	index := mongodriver.IndexModel{
		Keys: bson.D{
			{Key: fieldAgentID, Value: 1},
			{Key: fieldRunID, Value: 1},
			{Key: fieldCreatedAt, Value: 1},
			{Key: fieldID, Value: 1},
		},
	}
	_, err := coll.Indexes().CreateOne(ctx, index)
	return err
}

func newClientWithCollections(mongoClient *mongodriver.Client, legacyColl legacyCollection, eventColl eventCollection, timeout time.Duration) (*client, error) {
	if err := clientinfra.ValidateCollections("legacy collection is required", legacyColl); err != nil {
		return nil, err
	}
	if err := clientinfra.ValidateCollections("events collection is required", eventColl); err != nil {
		return nil, err
	}
	timeout = clientinfra.ResolveTimeout(timeout, defaultTimeout)
	return &client{
		mongo:      mongoClient,
		legacyColl: legacyColl,
		eventColl:  eventColl,
		timeout:    timeout,
	}, nil
}

type legacyCollection interface {
	clientinfra.FindOneCollection
	clientinfra.IndexedCollection
}

type eventCollection interface {
	clientinfra.InsertOneCollection
	clientinfra.FindCollection
	clientinfra.IndexedCollection
}

type singleResult = clientinfra.SingleResultDecoder

type cursor = clientinfra.CursorReader

type indexView = clientinfra.IndexCreator

func (c *client) loadLegacyRun(ctx context.Context, agentID, runID string) (runDocument, error) {
	var doc runDocument
	err := c.legacyColl.FindOne(ctx, bson.M{fieldAgentID: agentID, fieldRunID: runID}).Decode(&doc)
	if errors.Is(err, mongodriver.ErrNoDocuments) {
		return runDocument{}, nil
	}
	return doc, err
}

func (c *client) loadEventBuckets(ctx context.Context, agentID, runID string) (events []memory.Event, err error) {
	cur, err := c.eventColl.Find(
		ctx,
		bson.M{fieldAgentID: agentID, fieldRunID: runID},
		options.Find().SetSort(bson.D{
			{Key: fieldCreatedAt, Value: 1},
			{Key: fieldID, Value: 1},
		}),
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := cur.Close(ctx); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	for cur.Next(ctx) {
		var bucket eventBucketDocument
		if err := cur.Decode(&bucket); err != nil {
			return nil, err
		}
		events = append(events, fromEventDocuments(bucket.Events)...)
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return events, nil
}
