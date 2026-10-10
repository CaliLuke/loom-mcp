package mongo

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// Put implements runstore.Store.
func (c *client) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	if scope == "" {
		return "", fmt.Errorf("runstore put: scope is required")
	}
	if len(data) > MaxContentBytes {
		return "", fmt.Errorf("runstore put: %d bytes exceeds %d: %w", len(data), MaxContentBytes, runstore.ErrContentTooLarge)
	}
	hash := runstore.Hash(fmt.Sprintf("sha256:%x", sha256.Sum256(data)))
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	_, err := c.content.InsertOne(ctx, contentDocument{Scope: scope, Hash: hash, Data: data})
	if err != nil && !mongodriver.IsDuplicateKeyError(err) {
		return "", fmt.Errorf("runstore put scope %q hash %q: %w", scope, hash, err)
	}
	return hash, nil
}

// Get implements runstore.Store.
func (c *client) Get(ctx context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	if scope == "" {
		return nil, fmt.Errorf("runstore get: scope is required")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	var doc contentDocument
	err := c.content.FindOne(ctx, bson.M{fieldScope: scope, fieldHash: hash}).Decode(&doc)
	if errors.Is(err, mongodriver.ErrNoDocuments) {
		err = runstore.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("runstore get scope %q hash %q: %w", scope, hash, err)
	}
	return doc.Data, nil
}

// NextSeq implements runstore.Store.
func (c *client) NextSeq(ctx context.Context, scope, runID string) (int, error) {
	if scope == "" {
		return 0, fmt.Errorf("runstore next sequence: scope is required")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	var doc struct {
		Seq int `bson:"seq"`
	}
	err := c.counters.FindOneAndUpdate(ctx,
		bson.M{fieldScope: scope, fieldRunID: runID},
		bson.M{"$inc": bson.M{fieldSeq: 1}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&doc)
	if err != nil {
		return 0, fmt.Errorf("runstore next sequence scope %q run %q: %w", scope, runID, err)
	}
	return doc.Seq, nil
}

// DeleteScope implements runstore.Store.
func (c *client) DeleteScope(ctx context.Context, scope string) error {
	if scope == "" {
		return fmt.Errorf("runstore delete: scope is required")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	for _, coll := range []collection{c.content, c.calls, c.counters} {
		if _, err := coll.DeleteMany(ctx, bson.M{fieldScope: scope}); err != nil {
			return fmt.Errorf("runstore delete scope %q: %w", scope, err)
		}
	}
	return nil
}
