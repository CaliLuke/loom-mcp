package mongo

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

type continuationDocument struct {
	SessionID string `bson:"session_id"`
	ID        string `bson:"id"`
	Body      []byte `bson:"body"`
}

// AdvanceContinuation compares the immutable body before the expected head and
// commits insertion and head movement in one majority transaction.
func (c *client) AdvanceContinuation(ctx context.Context, sessionID string, expected *string, next session.Continuation) (session.AdvanceOutcome, error) {
	body, err := session.EncodeContinuation(next)
	if err != nil {
		return "", err
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	tx, err := c.mongo.StartSession()
	if err != nil {
		return "", err
	}
	defer tx.EndSession(context.WithoutCancel(ctx))
	result, err := tx.WithTransaction(ctx, func(txCtx context.Context) (any, error) {
		return c.advanceContinuation(txCtx, sessionID, expected, next, body)
	}, options.Transaction().SetReadConcern(readconcern.Majority()).SetWriteConcern(writeconcern.Majority()))
	if mongodriver.IsDuplicateKeyError(err) {
		stored, loadErr := c.loadContinuationBody(ctx, sessionID, next.ID)
		if loadErr != nil {
			return "", loadErr
		}
		if bytes.Equal(stored, body) {
			return session.AlreadyPublished, nil
		}
		return session.Conflict, nil
	}
	if err != nil {
		return "", err
	}
	return result.(session.AdvanceOutcome), nil
}

// LoadContinuationHead reads the head and immutable body in the same snapshot
// when called from AdvanceContinuation's transaction.
func (c *client) LoadContinuationHead(ctx context.Context, sessionID string) (*session.Continuation, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	var doc sessionDocument
	if err := c.sessions.FindOne(ctx, bson.M{fieldSessionID: sessionID}).Decode(&doc); err != nil {
		if errors.Is(err, mongodriver.ErrNoDocuments) {
			return nil, session.ErrSessionNotFound
		}
		return nil, err
	}
	if doc.ContinuationHead == nil {
		return nil, nil
	}
	next, err := c.LoadContinuation(ctx, sessionID, *doc.ContinuationHead)
	if err != nil {
		return nil, err
	}
	return &next, nil
}

// LoadContinuation reads only the requested session's immutable body.
func (c *client) LoadContinuation(ctx context.Context, sessionID, id string) (session.Continuation, error) {
	body, err := c.loadContinuationBody(ctx, sessionID, id)
	if err != nil {
		return session.Continuation{}, err
	}
	var next session.Continuation
	err = json.Unmarshal(body, &next)
	return next, err
}

func sameContinuationID(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
func (c *client) loadContinuationBody(ctx context.Context, sessionID, id string) ([]byte, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	var doc continuationDocument
	if err := c.continuations.FindOne(ctx, bson.M{fieldSessionID: sessionID, "id": id}).Decode(&doc); err != nil {
		if errors.Is(err, mongodriver.ErrNoDocuments) {
			return nil, session.ErrContinuationNotFound
		}
		return nil, err
	}
	return doc.Body, nil
}

func (c *client) advanceContinuation(ctx context.Context, sessionID string, expected *string, next session.Continuation, body []byte) (session.AdvanceOutcome, error) {
	old, err := c.loadContinuationBody(ctx, sessionID, next.ID)
	if err == nil {
		if bytes.Equal(old, body) {
			return session.AlreadyPublished, nil
		}
		return session.Conflict, nil
	}
	if !errors.Is(err, session.ErrContinuationNotFound) {
		return "", err
	}
	head, err := c.LoadContinuationHead(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if (head == nil && expected != nil) || (head != nil && (expected == nil || head.ID != *expected)) {
		return session.Superseded, nil
	}
	pos := 1
	if head != nil {
		pos = head.Pos + 1
	}
	if next.ID == "" || next.Pos != pos || !sameContinuationID(next.Prev, expected) {
		return "", fmt.Errorf("invalid continuation identity or position")
	}
	if _, err := c.continuations.InsertOne(ctx, continuationDocument{SessionID: sessionID, ID: next.ID, Body: body}); err != nil {
		return "", err
	}
	result, err := c.sessions.UpdateOne(ctx, bson.M{fieldSessionID: sessionID, "continuation_head": expected}, bson.M{mongoSetOperator: bson.M{"continuation_head": next.ID}})
	if err != nil {
		return "", err
	}
	if result.MatchedCount != 1 {
		return "", fmt.Errorf("continuation head changed inside transaction")
	}
	return session.Published, nil
}
