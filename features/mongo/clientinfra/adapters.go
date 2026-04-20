package clientinfra

import (
	"context"

	mongodriver "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// SingleResult adapts mongodriver.SingleResult to a feature-local interface.
type SingleResult struct {
	Res *mongodriver.SingleResult
}

// Cursor adapts mongodriver.Cursor to a feature-local interface.
type Cursor struct {
	Cur *mongodriver.Cursor
}

// IndexView adapts mongodriver.IndexView to a feature-local interface.
type IndexView struct {
	View mongodriver.IndexView
}

// Decode forwards to the wrapped SingleResult.
func (r SingleResult) Decode(val any) error {
	return r.Res.Decode(val)
}

// Next forwards to the wrapped Cursor.
func (c Cursor) Next(ctx context.Context) bool {
	return c.Cur.Next(ctx)
}

// Decode forwards to the wrapped Cursor.
func (c Cursor) Decode(val any) error {
	return c.Cur.Decode(val)
}

// Err forwards to the wrapped Cursor.
func (c Cursor) Err() error {
	return c.Cur.Err()
}

// Close forwards to the wrapped Cursor.
func (c Cursor) Close(ctx context.Context) error {
	return c.Cur.Close(ctx)
}

// CreateOne forwards to the wrapped IndexView.
func (v IndexView) CreateOne(ctx context.Context, model mongodriver.IndexModel, opts ...*options.CreateIndexesOptions) (string, error) {
	return v.View.CreateOne(ctx, model, opts...)
}
