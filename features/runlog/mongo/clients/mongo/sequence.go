package mongo

import (
	"context"
	"errors"
	"fmt"
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var errSequenceCollision = errors.New("runlog sequence collision")

// maxCommittedSequence reads the greatest sequence visible for one run.
func maxCommittedSequence(ctx context.Context, coll collection, runID string) (int64, error) {
	var doc eventDocument
	err := coll.FindOne(ctx, bson.M{
		fieldRunID:    runID,
		fieldSequence: bson.M{operatorExists: true},
	}, options.FindOne().SetSort(bson.D{{Key: fieldSequence, Value: -1}})).Decode(&doc)
	if errors.Is(err, mongodriver.ErrNoDocuments) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if doc.Sequence < 0 {
		return 0, fmt.Errorf("runlog sequence must be non-negative, got %d", doc.Sequence)
	}
	return doc.Sequence, nil
}

// sequenceCollision confirms that a unique sequence value is already committed.
func sequenceCollision(ctx context.Context, coll collection, runID string, sequence int64) (bool, error) {
	var doc eventDocument
	err := coll.FindOne(ctx, bson.M{
		fieldRunID:    runID,
		fieldSequence: sequence,
	}).Decode(&doc)
	if errors.Is(err, mongodriver.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// migrateLegacySequences assigns missing sequences in the old per-run ID order.
func migrateLegacySequences(ctx context.Context, coll collection) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var doc eventDocument
		err := coll.FindOne(ctx, bson.M{
			fieldSequence: bson.M{operatorExists: false},
		}, options.FindOne().SetSort(bson.D{
			{Key: fieldRunID, Value: 1},
			{Key: fieldID, Value: 1},
		})).Decode(&doc)
		if errors.Is(err, mongodriver.ErrNoDocuments) {
			return nil
		}
		if err != nil {
			return err
		}

		sequence, err := maxCommittedSequence(ctx, coll, doc.RunID)
		if err != nil {
			return err
		}
		if sequence == math.MaxInt64 {
			return fmt.Errorf("runlog sequence overflow while migrating run %q", doc.RunID)
		}
		sequence++
		result, err := coll.UpdateOne(ctx, bson.M{
			fieldID:       doc.ID,
			fieldRunID:    doc.RunID,
			fieldSequence: bson.M{operatorExists: false},
		}, bson.M{"$set": bson.M{fieldSequence: sequence}})
		if mongodriver.IsDuplicateKeyError(err) {
			collision, collisionErr := sequenceCollision(ctx, coll, doc.RunID, sequence)
			if collisionErr != nil {
				return collisionErr
			}
			if collision {
				continue
			}
		}
		if err != nil {
			return err
		}
		if result.MatchedCount == 0 {
			continue
		}
	}
}
