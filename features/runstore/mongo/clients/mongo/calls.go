package mongo

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// PutCall implements runstore.Store.
func (c *client) PutCall(ctx context.Context, scope string, record runstore.CallRecord) error {
	if scope == "" {
		return fmt.Errorf("runstore put call: scope is required")
	}
	if err := record.Validate(); err != nil {
		return err
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	if err := c.putCall(ctx, scope, record); err != nil {
		return fmt.Errorf("runstore put call scope %q key %+v: %w", scope, record.Key, err)
	}
	return nil
}

// ListCalls implements runstore.Store.
func (c *client) ListCalls(ctx context.Context, scope, runID string) (records []runstore.CallRecord, err error) {
	if scope == "" {
		return nil, fmt.Errorf("runstore list calls: scope is required")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	cur, err := c.calls.Find(ctx, bson.M{fieldScope: scope, fieldRunID: runID},
		options.Find().SetSort(bson.D{{Key: fieldSeq, Value: 1}, {Key: fieldAttempt, Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("runstore list calls scope %q run %q: %w", scope, runID, err)
	}
	defer func() {
		err = errors.Join(err, cur.Close(ctx))
		if err != nil {
			err = fmt.Errorf("runstore list calls scope %q run %q: %w", scope, runID, err)
		}
	}()
	for cur.Next(ctx) {
		var doc callDocument
		if err := cur.Decode(&doc); err != nil {
			return nil, err
		}
		records = append(records, runstore.CallRecord{
			Key:   runstore.CallKey{RunID: doc.RunID, Seq: doc.Seq, Attempt: doc.Attempt},
			State: doc.State, Started: doc.Started, Completion: doc.Completion,
		})
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func callFilter(scope string, key runstore.CallKey) bson.M {
	return bson.M{fieldScope: scope, fieldRunID: key.RunID, fieldSeq: key.Seq, fieldAttempt: key.Attempt}
}

// putCall uses insertion for starts and compare-and-set for completion. Reads
// after a duplicate or unmatched update classify retries without another write.
func (c *client) putCall(ctx context.Context, scope string, record runstore.CallRecord) error {
	if record.State == runstore.CallStarted {
		_, err := c.calls.InsertOne(ctx, callDocument{
			Scope: scope, RunID: record.Key.RunID, Seq: record.Key.Seq, Attempt: record.Key.Attempt,
			State: record.State, Started: record.Started, Completion: record.Completion,
		})
		if err == nil || !mongodriver.IsDuplicateKeyError(err) {
			return err
		}
	} else {
		filter := callFilter(scope, record.Key)
		filter[fieldState] = runstore.CallStarted
		filter[fieldStarted] = record.Started
		result, err := c.calls.UpdateOne(ctx, filter, bson.M{"$set": bson.M{
			fieldState: record.State, fieldCompletion: record.Completion,
		}})
		if err != nil {
			return err
		}
		if result.MatchedCount != 0 {
			return nil
		}
	}
	return c.resolveCallWrite(ctx, scope, record)
}

// resolveCallWrite compares opaque bytes after an insert collision or failed
// conditional completion; a final record can only be replayed exactly.
func (c *client) resolveCallWrite(ctx context.Context, scope string, record runstore.CallRecord) error {
	var existing callDocument
	err := c.calls.FindOne(ctx, callFilter(scope, record.Key)).Decode(&existing)
	if errors.Is(err, mongodriver.ErrNoDocuments) {
		return runstore.ErrCallTransition
	}
	if err != nil {
		return err
	}
	if record.State == runstore.CallStarted && existing.State != runstore.CallStarted {
		return runstore.ErrCallTransition
	}
	if !bytes.Equal(existing.Started, record.Started) || existing.State != record.State ||
		!bytes.Equal(existing.Completion, record.Completion) {
		return runstore.ErrCallConflict
	}
	return nil
}
