package mongo

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/CaliLuke/loom-mcp/v2/features/mongo/clientinfra"
)

type (
	fakeCollection struct {
		t                                                  *testing.T
		mu                                                 sync.Mutex
		docs                                               []bson.M
		indexes                                            []mongodriver.IndexModel
		writes                                             int
		insertErr, readErr, updateErr, deleteErr, indexErr error
		decodeErr, cursorErr, closeErr                     error
	}
	fakeResult struct {
		t   *testing.T
		doc bson.M
		err error
	}
	fakeCursor struct {
		t                              *testing.T
		docs                           []bson.M
		pos                            int
		decodeErr, cursorErr, closeErr error
	}
)

func duplicateKey() error {
	return mongodriver.WriteException{WriteErrors: []mongodriver.WriteError{{Code: 11000, Message: "duplicate key"}}}
}

func concreteCollection(coll collection) *mongodriver.Collection {
	return coll.(clientinfra.Collection).Coll
}

func applyOptions[T any](t *testing.T, opts ...options.Lister[T]) *T {
	t.Helper()
	out := new(T)
	for _, opt := range opts {
		for _, apply := range opt.List() {
			require.NoError(t, apply(out))
		}
	}
	return out
}

// bsonMap reproduces the driver's ownership boundary and normalizes filter
// values and documents to the same BSON types (including binary bodies).
func bsonMap(t *testing.T, value any) bson.M {
	t.Helper()
	data, err := bson.Marshal(value)
	require.NoError(t, err)
	var out bson.M
	require.NoError(t, bson.Unmarshal(data, &out))
	return out
}

func matches(doc, filter bson.M) bool {
	for key, value := range filter {
		if !reflect.DeepEqual(doc[key], value) {
			return false
		}
	}
	return true
}

func (f *fakeCollection) InsertOne(_ context.Context, value any, _ ...options.Lister[options.InsertOneOptions]) (*mongodriver.InsertOneResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	if f.insertErr != nil {
		return nil, f.insertErr
	}
	doc := bsonMap(f.t, value)
	for _, index := range f.indexes {
		identity := bson.M{}
		for _, field := range index.Keys.(bson.D) {
			identity[field.Key] = doc[field.Key]
		}
		for _, existing := range f.docs {
			if matches(existing, identity) {
				return nil, duplicateKey()
			}
		}
	}
	f.docs = append(f.docs, doc)
	return &mongodriver.InsertOneResult{}, nil
}

func (f *fakeCollection) FindOne(_ context.Context, filter any, _ ...options.Lister[options.FindOneOptions]) clientinfra.SingleResultDecoder {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return fakeResult{err: f.readErr}
	}
	normalized := bsonMap(f.t, filter)
	for _, doc := range f.docs {
		if matches(doc, normalized) {
			return fakeResult{t: f.t, doc: bsonMap(f.t, doc)}
		}
	}
	return fakeResult{err: mongodriver.ErrNoDocuments}
}

func (f *fakeCollection) Find(_ context.Context, filter any, opts ...options.Lister[options.FindOptions]) (clientinfra.CursorReader, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return nil, f.readErr
	}
	var docs []bson.M
	normalized := bsonMap(f.t, filter)
	for _, doc := range f.docs {
		if matches(doc, normalized) {
			docs = append(docs, bsonMap(f.t, doc))
		}
	}
	find := applyOptions(f.t, opts...)
	if find.Sort != nil {
		slices.SortFunc(docs, func(a, b bson.M) int {
			for _, field := range find.Sort.(bson.D) {
				av, bv := a[field.Key].(int32), b[field.Key].(int32)
				if av < bv {
					return -1
				}
				if av > bv {
					return 1
				}
			}
			return 0
		})
	}
	return &fakeCursor{t: f.t, docs: docs, decodeErr: f.decodeErr, cursorErr: f.cursorErr, closeErr: f.closeErr}, nil
}

func (f *fakeCollection) UpdateOne(_ context.Context, filter, update any, _ ...options.Lister[options.UpdateOneOptions]) (*mongodriver.UpdateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	normalized := bsonMap(f.t, filter)
	set := update.(bson.M)["$set"].(bson.M)
	for _, doc := range f.docs {
		if matches(doc, normalized) {
			for key, value := range bsonMap(f.t, set) {
				doc[key] = value
			}
			return &mongodriver.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil
		}
	}
	return &mongodriver.UpdateResult{}, nil
}

func (f *fakeCollection) FindOneAndUpdate(_ context.Context, filter, update any, opts ...options.Lister[options.FindOneAndUpdateOptions]) clientinfra.SingleResultDecoder {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	if f.updateErr != nil {
		return fakeResult{err: f.updateErr}
	}
	config := applyOptions(f.t, opts...)
	require.NotNil(f.t, config.Upsert)
	require.True(f.t, *config.Upsert)
	require.NotNil(f.t, config.ReturnDocument)
	require.Equal(f.t, options.After, *config.ReturnDocument)
	inc := bsonMap(f.t, update.(bson.M)["$inc"])
	normalized := bsonMap(f.t, filter)
	var found bson.M
	for _, doc := range f.docs {
		if matches(doc, normalized) {
			found = doc
			break
		}
	}
	if found == nil {
		found = normalized
		f.docs = append(f.docs, found)
	}
	for key, value := range inc {
		current, exists := found[key]
		if !exists {
			current = int32(0)
		}
		found[key] = current.(int32) + value.(int32)
	}
	return fakeResult{t: f.t, doc: bsonMap(f.t, found)}
}

func (f *fakeCollection) DeleteMany(_ context.Context, filter any, _ ...options.Lister[options.DeleteManyOptions]) (*mongodriver.DeleteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	normalized := bsonMap(f.t, filter)
	before := len(f.docs)
	f.docs = slices.DeleteFunc(f.docs, func(doc bson.M) bool {
		return matches(doc, normalized)
	})
	return &mongodriver.DeleteResult{DeletedCount: int64(before - len(f.docs))}, nil
}

func (f *fakeCollection) Indexes() clientinfra.IndexCreator {
	return f
}

func (f *fakeCollection) CreateOne(_ context.Context, model mongodriver.IndexModel, _ ...options.Lister[options.CreateIndexesOptions]) (string, error) {
	if f.indexErr != nil {
		return "", f.indexErr
	}
	f.indexes = append(f.indexes, model)
	return "index", nil
}

func (r fakeResult) Decode(value any) error {
	if r.err != nil {
		return r.err
	}
	data, err := bson.Marshal(r.doc)
	if err != nil {
		return err
	}
	return bson.Unmarshal(data, value)
}

func (c *fakeCursor) Next(context.Context) bool {
	if c.pos == len(c.docs) {
		return false
	}
	c.pos++
	return true
}

func (c *fakeCursor) Decode(value any) error {
	if c.decodeErr != nil {
		return c.decodeErr
	}
	return (fakeResult{t: c.t, doc: c.docs[c.pos-1]}).Decode(value)
}

func (c *fakeCursor) Err() error {
	return c.cursorErr
}

func (c *fakeCursor) Close(context.Context) error {
	return c.closeErr
}
