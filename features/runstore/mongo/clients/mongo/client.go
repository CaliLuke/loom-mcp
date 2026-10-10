// Package mongo implements the low-level MongoDB client for run stores.
package mongo

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/CaliLuke/loom/clue/health"

	"github.com/CaliLuke/loom-mcp/v2/features/mongo/clientinfra"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

type (
	// Client exposes Mongo-backed run store operations and a health check.
	Client interface {
		health.Pinger
		runstore.Store
	}

	// Options configures the Mongo run store. Empty collection names use
	// agent_run_content, agent_run_calls and agent_run_sequences respectively.
	// A non-positive Timeout uses five seconds.
	Options struct {
		Client            *mongodriver.Client
		Database          string
		ContentCollection string
		CallCollection    string
		CounterCollection string
		Timeout           time.Duration
	}

	collectionCloner func(*mongodriver.Collection, ...options.Lister[options.CollectionOptions]) *mongodriver.Collection

	client struct {
		mongo    *mongodriver.Client
		content  collection
		calls    collection
		counters collection
		timeout  time.Duration
	}

	collection interface {
		clientinfra.InsertOneCollection
		clientinfra.FindOneCollection
		clientinfra.FindCollection
		clientinfra.UpdateOneCollection
		clientinfra.FindOneAndUpdateCollection
		clientinfra.DeleteManyCollection
		clientinfra.IndexedCollection
	}

	contentDocument struct {
		Scope string        `bson:"scope"`
		Hash  runstore.Hash `bson:"hash"`
		Data  []byte        `bson:"data"`
	}

	callDocument struct {
		Scope      string             `bson:"scope"`
		RunID      string             `bson:"run_id"`
		Seq        int                `bson:"seq"`
		Attempt    int                `bson:"attempt"`
		State      runstore.CallState `bson:"state"`
		Started    []byte             `bson:"started"`
		Completion []byte             `bson:"completion"`
	}
)

const (
	// MaxContentBytes is the maximum Put payload (15 MiB), leaving room for
	// document metadata below MongoDB's 16 MiB BSON document limit.
	MaxContentBytes = 15 * 1024 * 1024
	defaultTimeout  = 5 * time.Second
	fieldScope      = "scope"
	fieldHash       = "hash"
	fieldRunID      = "run_id"
	fieldSeq        = "seq"
	fieldAttempt    = "attempt"
	fieldState      = "state"
	fieldStarted    = "started"
	fieldCompletion = "completion"
)

// New validates options, clones all collections with majority write concern and
// primary read preference, and ensures the three unique identity indexes.
func New(opts Options) (Client, error) {
	if err := clientinfra.ValidateMongoOptions(opts.Client, opts.Database); err != nil {
		return nil, fmt.Errorf("runstore mongo: %w", err)
	}
	content, calls, counters := configuredCollections(opts, (*mongodriver.Collection).Clone)
	timeout := clientinfra.ResolveTimeout(opts.Timeout, defaultTimeout)
	if err := clientinfra.EnsureIndexes(timeout, func(ctx context.Context) error {
		return ensureIndexes(ctx, content, calls, counters)
	}); err != nil {
		return nil, fmt.Errorf("runstore mongo indexes: %w", err)
	}
	return &client{mongo: opts.Client, content: content, calls: calls, counters: counters, timeout: timeout}, nil
}

// Name identifies the run store health check.
func (c *client) Name() string {
	return "runstore-mongo"
}

// Ping checks connectivity to the primary.
func (c *client) Ping(ctx context.Context) error {
	return clientinfra.Ping(ctx, c.mongo, false)
}

// configuredCollections applies durability and read routing to each clone,
// independent of database or client defaults.
func configuredCollections(opts Options, clone collectionCloner) (clientinfra.Collection, clientinfra.Collection, clientinfra.Collection) {
	makeCollection := func(name, fallback string) clientinfra.Collection {
		coll := clientinfra.NewCollection(opts.Client, opts.Database, clientinfra.ResolveCollectionName(name, fallback))
		coll.Coll = clone(coll.Coll, options.Collection().
			SetWriteConcern(writeconcern.Majority()).SetReadPreference(readpref.Primary()))
		return coll
	}
	return makeCollection(opts.ContentCollection, "agent_run_content"),
		makeCollection(opts.CallCollection, "agent_run_calls"),
		makeCollection(opts.CounterCollection, "agent_run_sequences")
}

// ensureIndexes makes each content, call, and counter identity unique.
func ensureIndexes(ctx context.Context, content, calls, counters collection) error {
	for _, index := range []struct {
		coll collection
		keys bson.D
	}{
		{content, bson.D{{Key: fieldScope, Value: 1}, {Key: fieldHash, Value: 1}}},
		{calls, bson.D{{Key: fieldScope, Value: 1}, {Key: fieldRunID, Value: 1}, {Key: fieldSeq, Value: 1}, {Key: fieldAttempt, Value: 1}}},
		{counters, bson.D{{Key: fieldScope, Value: 1}, {Key: fieldRunID, Value: 1}}},
	} {
		if _, err := index.coll.Indexes().CreateOne(ctx, mongodriver.IndexModel{
			Keys: index.keys, Options: options.Index().SetUnique(true),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (c *client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return clientinfra.WithTimeout(ctx, c.timeout, false)
}
