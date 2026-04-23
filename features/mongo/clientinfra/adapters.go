package clientinfra

import (
	"context"

	mongodriver "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type (
	// SingleResultDecoder is the common decode-only shape of Mongo single-result reads.
	SingleResultDecoder interface {
		Decode(val any) error
	}

	// CursorReader is the common cursor shape used by feature clients.
	CursorReader interface {
		Next(ctx context.Context) bool
		Decode(val any) error
		Err() error
		Close(ctx context.Context) error
	}

	// IndexCreator is the common index creation shape used by feature clients.
	IndexCreator interface {
		CreateOne(ctx context.Context, model mongodriver.IndexModel, opts ...*options.CreateIndexesOptions) (string, error)
	}

	// FindOneCollection captures the common Mongo FindOne operation.
	FindOneCollection interface {
		FindOne(ctx context.Context, filter any, opts ...*options.FindOneOptions) SingleResultDecoder
	}

	// FindCollection captures the common Mongo Find operation.
	FindCollection interface {
		Find(ctx context.Context, filter any, opts ...*options.FindOptions) (CursorReader, error)
	}

	// InsertOneCollection captures the common Mongo InsertOne operation.
	InsertOneCollection interface {
		InsertOne(ctx context.Context, document any, opts ...*options.InsertOneOptions) (*mongodriver.InsertOneResult, error)
	}

	// UpdateOneCollection captures the common Mongo UpdateOne operation.
	UpdateOneCollection interface {
		UpdateOne(ctx context.Context, filter any, update any, opts ...*options.UpdateOptions) (*mongodriver.UpdateResult, error)
	}

	// IndexedCollection captures access to Mongo index management.
	IndexedCollection interface {
		Indexes() IndexCreator
	}
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

// Collection adapts mongodriver.Collection to feature-local collection interfaces.
type Collection struct {
	Coll *mongodriver.Collection
}

// NewCollection returns the common adapter for a concrete Mongo collection.
func NewCollection(client *mongodriver.Client, database string, collection string) Collection {
	return Collection{Coll: client.Database(database).Collection(collection)}
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

// FindOne forwards to the wrapped Collection.
func (c Collection) FindOne(ctx context.Context, filter any, opts ...*options.FindOneOptions) SingleResultDecoder {
	return SingleResult{Res: c.Coll.FindOne(ctx, filter, opts...)}
}

// Find forwards to the wrapped Collection.
func (c Collection) Find(ctx context.Context, filter any, opts ...*options.FindOptions) (CursorReader, error) {
	cur, err := c.Coll.Find(ctx, filter, opts...)
	if err != nil {
		return nil, err
	}
	return Cursor{Cur: cur}, nil
}

// InsertOne forwards to the wrapped Collection.
func (c Collection) InsertOne(ctx context.Context, document any, opts ...*options.InsertOneOptions) (*mongodriver.InsertOneResult, error) {
	return c.Coll.InsertOne(ctx, document, opts...)
}

// UpdateOne forwards to the wrapped Collection.
func (c Collection) UpdateOne(ctx context.Context, filter any, update any, opts ...*options.UpdateOptions) (*mongodriver.UpdateResult, error) {
	return c.Coll.UpdateOne(ctx, filter, update, opts...)
}

// Indexes forwards to the wrapped Collection.
func (c Collection) Indexes() IndexCreator {
	return IndexView{View: c.Coll.Indexes()}
}
