// Package mongo wires runstore.Store to the MongoDB client.
package mongo

import (
	"context"
	"errors"

	clientsmongo "github.com/CaliLuke/loom-mcp/v2/features/runstore/mongo/clients/mongo"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// Store implements runstore.Store by delegating to the Mongo client.
type Store struct {
	client clientsmongo.Client
}

// NewStore builds a Mongo run store using the provided client.
func NewStore(client clientsmongo.Client) (*Store, error) {
	if client == nil {
		return nil, errors.New("client is required")
	}
	return &Store{client: client}, nil
}

// Put implements runstore.Store.
func (s *Store) Put(ctx context.Context, scope string, data []byte) (runstore.Hash, error) {
	return s.client.Put(ctx, scope, data)
}

// Get implements runstore.Store.
func (s *Store) Get(ctx context.Context, scope string, hash runstore.Hash) ([]byte, error) {
	return s.client.Get(ctx, scope, hash)
}

// NextSeq implements runstore.Store.
func (s *Store) NextSeq(ctx context.Context, scope, runID string) (int, error) {
	return s.client.NextSeq(ctx, scope, runID)
}

// PutCall implements runstore.Store.
func (s *Store) PutCall(ctx context.Context, scope string, record runstore.CallRecord) error {
	return s.client.PutCall(ctx, scope, record)
}

// ListCalls implements runstore.Store.
func (s *Store) ListCalls(ctx context.Context, scope, runID string) ([]runstore.CallRecord, error) {
	return s.client.ListCalls(ctx, scope, runID)
}

// DeleteScope implements runstore.Store.
func (s *Store) DeleteScope(ctx context.Context, scope string) error {
	return s.client.DeleteScope(ctx, scope)
}
