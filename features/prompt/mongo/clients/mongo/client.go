// Package mongo implements the low-level MongoDB client used by the prompt
// override store.
package mongo

//go:generate cmg gen .

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/CaliLuke/loom/clue/health"

	clientinfra "github.com/CaliLuke/loom-mcp/features/mongo/clientinfra"
	"github.com/CaliLuke/loom-mcp/runtime/agent/prompt"
)

type (
	// Client exposes Mongo-backed operations for prompt overrides.
	Client interface {
		health.Pinger

		Resolve(ctx context.Context, promptID prompt.Ident, scope prompt.Scope) (*prompt.Override, error)
		Set(ctx context.Context, promptID prompt.Ident, scope prompt.Scope, template string, metadata map[string]string) error
		History(ctx context.Context, promptID prompt.Ident) ([]*prompt.Override, error)
		List(ctx context.Context) ([]*prompt.Override, error)
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

	overrideDocument struct {
		ID               bson.ObjectID     `bson:"_id,omitempty"`
		PromptID         string            `bson:"prompt_id"`
		ScopeSession     string            `bson:"scope_session"`
		ScopeLabels      map[string]string `bson:"scope_labels,omitempty"`
		ScopeLabelCount  int               `bson:"scope_label_count"`
		ScopeFingerprint string            `bson:"scope_fingerprint"`
		Template         string            `bson:"template"`
		Version          string            `bson:"version"`
		CreatedAt        time.Time         `bson:"created_at"`
		Metadata         map[string]string `bson:"metadata,omitempty"`
	}

	resolveQuery struct {
		filter bson.M
		limit  int64
	}
)

const (
	defaultCollection     = "prompt_overrides"
	defaultTimeout        = 5 * time.Second
	clientName            = "prompt-mongo"
	fieldPromptID         = "prompt_id"
	fieldScopeSession     = "scope_session"
	fieldScopeLabels      = "scope_labels"
	fieldScopeLabelCount  = "scope_label_count"
	fieldScopeFingerprint = "scope_fingerprint"
	fieldCreatedAt        = "created_at"
	maxIndexedScopeLabels = 15
	fieldID               = "_id"
)

// New returns a Client backed by the provided MongoDB client.
func New(opts Options) (Client, error) {
	if err := clientinfra.ValidateMongoOptions(opts.Client, opts.Database); err != nil {
		return nil, err
	}
	collection := clientinfra.ResolveCollectionName(opts.Collection, defaultCollection)
	timeout := clientinfra.ResolveTimeout(opts.Timeout, defaultTimeout)

	wrapper := clientinfra.NewCollection(opts.Client, opts.Database, collection)
	if err := clientinfra.EnsureIndexes(timeout, func(ctx context.Context) error {
		if err := backfillScopeFingerprints(ctx, wrapper); err != nil {
			return err
		}
		return ensureIndexes(ctx, wrapper)
	}); err != nil {
		return nil, err
	}
	return newClientWithCollection(opts.Client, wrapper, timeout)
}

func (c *client) Name() string {
	return clientName
}

func (c *client) Ping(ctx context.Context) error {
	return clientinfra.Ping(ctx, c.mongo, true)
}

func (c *client) Resolve(ctx context.Context, promptID prompt.Ident, scope prompt.Scope) (*prompt.Override, error) {
	if promptID == "" {
		return nil, errors.New("prompt id is required")
	}
	if err := validateIndexedScope(scope); err != nil {
		return nil, err
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	for _, query := range resolveQueries(promptID, scope) {
		override, err := c.findLatestMatchingOverride(ctx, query)
		if err != nil {
			return nil, err
		}
		if override != nil {
			return override, nil
		}
	}
	return nil, nil
}

func (c *client) findLatestMatchingOverride(ctx context.Context, query resolveQuery) (*prompt.Override, error) {
	opts := options.Find().SetSort(bson.D{
		{Key: fieldScopeLabelCount, Value: -1},
		{Key: fieldCreatedAt, Value: -1},
		{Key: fieldID, Value: -1},
	})
	if query.limit > 0 {
		opts.SetLimit(query.limit)
	}
	cur, err := c.coll.Find(ctx, query.filter, opts)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = cur.Close(ctx)
	}()
	overrides, err := decodeOverrides(ctx, cur)
	if err != nil {
		return nil, err
	}
	if len(overrides) > 0 {
		return overrides[0], nil
	}
	return nil, nil
}

func resolveQueries(promptID prompt.Ident, scope prompt.Scope) []resolveQuery {
	fingerprints := scopeCandidateFingerprints(scope.Labels)
	queries := make([]resolveQuery, 0, 2)
	if scope.SessionID != "" {
		queries = append(queries, resolveQueryFor(promptID, scope.SessionID, fingerprints))
	}
	queries = append(queries, resolveQueryFor(promptID, "", fingerprints))
	return queries
}

func resolveQueryFor(promptID prompt.Ident, sessionID string, fingerprints []string) resolveQuery {
	return resolveQuery{
		filter: bson.M{
			fieldPromptID:         promptID.String(),
			fieldScopeSession:     sessionID,
			fieldScopeFingerprint: bson.M{"$in": fingerprints},
		},
		limit: 1,
	}
}

func scopeCandidateFingerprints(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	candidates := make([]string, 0, 1<<len(keys))
	scope := make(map[string]string, len(keys))
	var addSubsets func(int)
	addSubsets = func(index int) {
		if index == len(keys) {
			candidates = append(candidates, prompt.ScopeFingerprint(scope))
			return
		}
		addSubsets(index + 1)
		key := keys[index]
		scope[key] = labels[key]
		addSubsets(index + 1)
		delete(scope, key)
	}
	addSubsets(0)
	return candidates
}

func validateIndexedScope(scope prompt.Scope) error {
	if len(scope.Labels) > maxIndexedScopeLabels {
		return fmt.Errorf("prompt scope has %d labels; Mongo prompt scopes support at most %d", len(scope.Labels), maxIndexedScopeLabels)
	}
	return nil
}

func (c *client) Set(ctx context.Context, promptID prompt.Ident, scope prompt.Scope, template string, metadata map[string]string) error {
	if promptID == "" {
		return errors.New("prompt id is required")
	}
	if template == "" {
		return errors.New("template is required")
	}
	if err := validateIndexedScope(scope); err != nil {
		return err
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	doc := overrideDocument{
		PromptID:         promptID.String(),
		ScopeSession:     scope.SessionID,
		ScopeLabels:      cloneMetadata(scope.Labels),
		ScopeLabelCount:  len(scope.Labels),
		ScopeFingerprint: prompt.ScopeFingerprint(scope.Labels),
		Template:         template,
		Version:          prompt.VersionFromTemplate(template),
		CreatedAt:        time.Now().UTC(),
		Metadata:         cloneMetadata(metadata),
	}
	_, err := c.coll.InsertOne(ctx, doc)
	return err
}

func (c *client) History(ctx context.Context, promptID prompt.Ident) ([]*prompt.Override, error) {
	if promptID == "" {
		return nil, errors.New("prompt id is required")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	cur, err := c.coll.Find(ctx, bson.M{fieldPromptID: promptID.String()}, options.Find().SetSort(bson.D{{Key: fieldCreatedAt, Value: -1}}))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = cur.Close(ctx)
	}()
	return decodeOverrides(ctx, cur)
}

func (c *client) List(ctx context.Context) ([]*prompt.Override, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	cur, err := c.coll.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: fieldCreatedAt, Value: -1}}))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = cur.Close(ctx)
	}()
	return decodeOverrides(ctx, cur)
}

func (c *client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return clientinfra.WithTimeout(ctx, c.timeout, true)
}

func decodeOverrides(ctx context.Context, cur cursor) ([]*prompt.Override, error) {
	overrides := make([]*prompt.Override, 0)
	for cur.Next(ctx) {
		var doc overrideDocument
		if err := cur.Decode(&doc); err != nil {
			return nil, err
		}
		overrides = append(overrides, toOverride(doc))
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return overrides, nil
}

func toOverride(doc overrideDocument) *prompt.Override {
	return &prompt.Override{
		PromptID: prompt.Ident(doc.PromptID),
		Scope: prompt.Scope{
			SessionID: doc.ScopeSession,
			Labels:    cloneMetadata(doc.ScopeLabels),
		},
		Template:  doc.Template,
		Version:   doc.Version,
		CreatedAt: doc.CreatedAt,
		Metadata:  cloneMetadata(doc.Metadata),
	}
}

func cloneMetadata(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, val := range src {
		dst[key] = val
	}
	return dst
}

func backfillScopeFingerprints(ctx context.Context, coll collection) (retErr error) {
	cur, err := coll.Find(ctx, bson.M{
		"$or": bson.A{
			bson.M{fieldScopeFingerprint: bson.M{"$exists": false}},
			bson.M{fieldScopeFingerprint: ""},
		},
	})
	if err != nil {
		return fmt.Errorf("find legacy prompt scopes: %w", err)
	}
	defer func() {
		if err := cur.Close(ctx); err != nil && retErr == nil {
			retErr = fmt.Errorf("close legacy prompt scope cursor: %w", err)
		}
	}()
	for cur.Next(ctx) {
		var doc overrideDocument
		if err := cur.Decode(&doc); err != nil {
			return fmt.Errorf("decode legacy prompt scope: %w", err)
		}
		if err := validateIndexedScope(prompt.Scope{Labels: doc.ScopeLabels}); err != nil {
			return fmt.Errorf("backfill prompt %q: %w", doc.PromptID, err)
		}
		fingerprint := prompt.ScopeFingerprint(doc.ScopeLabels)
		if _, err := coll.UpdateOne(ctx,
			bson.M{fieldID: doc.ID},
			bson.M{"$set": bson.M{fieldScopeFingerprint: fingerprint}},
		); err != nil {
			return fmt.Errorf("backfill prompt %q scope fingerprint: %w", doc.PromptID, err)
		}
	}
	if err := cur.Err(); err != nil {
		return fmt.Errorf("scan legacy prompt scopes: %w", err)
	}
	return nil
}

func ensureIndexes(ctx context.Context, coll collection) error {
	lookup := mongodriver.IndexModel{
		Keys: bson.D{
			{Key: fieldPromptID, Value: 1},
			{Key: fieldScopeSession, Value: 1},
			{Key: fieldScopeFingerprint, Value: 1},
			{Key: fieldScopeLabelCount, Value: -1},
			{Key: fieldCreatedAt, Value: -1},
			{Key: fieldID, Value: -1},
		},
	}
	if _, err := coll.Indexes().CreateOne(ctx, lookup); err != nil {
		return err
	}
	history := mongodriver.IndexModel{
		Keys: bson.D{
			{Key: fieldPromptID, Value: 1},
			{Key: fieldCreatedAt, Value: -1},
		},
	}
	if _, err := coll.Indexes().CreateOne(ctx, history); err != nil {
		return err
	}
	return nil
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
	clientinfra.FindOneCollection
	clientinfra.FindCollection
	clientinfra.InsertOneCollection
	clientinfra.UpdateOneCollection
	clientinfra.IndexedCollection
}

type singleResult = clientinfra.SingleResultDecoder

type cursor = clientinfra.CursorReader

type indexView = clientinfra.IndexCreator
