package mongo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/runstoretest"
)

func TestClientConformance(t *testing.T) {
	runstoretest.RunConformance(t, func(t *testing.T) runstore.Store {
		return newFakeClient(t)
	})
}

func TestContentLimit(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
		want error
	}{
		{"at_limit", MaxContentBytes, nil},
		{"over_limit", MaxContentBytes + 1, runstore.ErrContentTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newFakeClient(t)
			hash, err := c.Put(t.Context(), "scope", make([]byte, tc.size))
			coll := c.content.(*fakeCollection)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				assert.Empty(t, hash)
				assert.Empty(t, coll.docs)
				assert.Zero(t, coll.writes)
				return
			}
			require.NoError(t, err)
			data, err := c.Get(t.Context(), "scope", hash)
			require.NoError(t, err)
			assert.Len(t, data, tc.size)
		})
	}
}

func TestCollectionOptions(t *testing.T) {
	driver, err := mongodriver.Connect(options.Client().SetReadPreference(readpref.Secondary()).SetWriteConcern(writeconcern.Unacknowledged()))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, driver.Disconnect(context.Background()))
	})
	for _, tc := range []struct {
		name  string
		opts  Options
		names []string
	}{
		{"defaults", Options{Client: driver, Database: "test"}, []string{"agent_run_content", "agent_run_calls", "agent_run_sequences"}},
		{"custom", Options{Client: driver, Database: "test", ContentCollection: "content", CallCollection: "calls", CounterCollection: "seq"}, []string{"content", "calls", "seq"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var applied []*options.CollectionOptions
			content, calls, counters := configuredCollections(tc.opts, func(coll *mongodriver.Collection, opts ...options.Lister[options.CollectionOptions]) *mongodriver.Collection {
				applied = append(applied, applyOptions(t, opts...))
				return coll.Clone(opts...)
			})
			require.Len(t, applied, 3)
			for i, coll := range []collection{content, calls, counters} {
				// Inspect the options passed to each Clone, overriding client defaults.
				concrete := concreteCollection(coll)
				assert.Equal(t, tc.names[i], concrete.Name())
				assert.Equal(t, writeconcern.Majority(), applied[i].WriteConcern)
				assert.Equal(t, readpref.PrimaryMode, applied[i].ReadPreference.Mode())
			}
		})
	}
}

func TestNewValidation(t *testing.T) {
	for _, opts := range []Options{{}, {Client: &mongodriver.Client{}}} {
		_, err := New(opts)
		assert.Error(t, err)
	}
}

func TestIndexes(t *testing.T) {
	c := newFakeClient(t)
	for i, coll := range []*fakeCollection{c.content.(*fakeCollection), c.calls.(*fakeCollection), c.counters.(*fakeCollection)} {
		require.Len(t, coll.indexes, 1)
		want := []bson.D{
			{{Key: "scope", Value: 1}, {Key: "hash", Value: 1}},
			{{Key: "scope", Value: 1}, {Key: "run_id", Value: 1}, {Key: "seq", Value: 1}, {Key: "attempt", Value: 1}},
			{{Key: "scope", Value: 1}, {Key: "run_id", Value: 1}},
		}
		assert.Equal(t, want[i], coll.indexes[0].Keys)
		opts := applyOptions(t, coll.indexes[0].Options)
		require.NotNil(t, opts.Unique)
		assert.True(t, *opts.Unique)
	}
}

func TestClientErrors(t *testing.T) {
	sentinel := errors.New("mongo unavailable")
	for _, tc := range []struct {
		name  string
		setup func(*client)
		run   func(*testing.T, *client) error
	}{
		{"put", func(c *client) {
			c.content.(*fakeCollection).insertErr = sentinel
		}, func(t *testing.T, c *client) error {
			_, err := c.Put(t.Context(), "scope", []byte("body"))
			return err
		}},
		{"get", func(c *client) {
			c.content.(*fakeCollection).readErr = sentinel
		}, func(t *testing.T, c *client) error {
			_, err := c.Get(t.Context(), "scope", "hash")
			return err
		}},
		{"sequence", func(c *client) {
			c.counters.(*fakeCollection).updateErr = sentinel
		}, func(t *testing.T, c *client) error {
			_, err := c.NextSeq(t.Context(), "scope", "run")
			return err
		}},
		{"started", func(c *client) {
			c.calls.(*fakeCollection).insertErr = sentinel
		}, func(t *testing.T, c *client) error {
			return c.PutCall(t.Context(), "scope", testCall(runstore.CallStarted))
		}},
		{"completion", func(c *client) {
			c.calls.(*fakeCollection).updateErr = sentinel
		}, func(t *testing.T, c *client) error {
			return c.PutCall(t.Context(), "scope", testCall(runstore.CallCompleted))
		}},
		{"completion_read", func(c *client) {
			c.calls.(*fakeCollection).readErr = sentinel
		}, func(t *testing.T, c *client) error {
			return c.PutCall(t.Context(), "scope", testCall(runstore.CallCompleted))
		}},
		{"duplicate_read", func(c *client) {
			c.calls.(*fakeCollection).insertErr = duplicateKey()
			c.calls.(*fakeCollection).readErr = sentinel
		}, func(t *testing.T, c *client) error {
			return c.PutCall(t.Context(), "scope", testCall(runstore.CallStarted))
		}},
		{"list", func(c *client) {
			c.calls.(*fakeCollection).readErr = sentinel
		}, func(t *testing.T, c *client) error {
			_, err := c.ListCalls(t.Context(), "scope", "run")
			return err
		}},
		{"cursor_decode", func(c *client) {
			c.calls.(*fakeCollection).decodeErr = sentinel
		}, func(t *testing.T, c *client) error {
			require.NoError(t, c.PutCall(t.Context(), "scope", testCall(runstore.CallStarted)))
			_, err := c.ListCalls(t.Context(), "scope", "run")
			return err
		}},
		{"cursor_error", func(c *client) {
			c.calls.(*fakeCollection).cursorErr = sentinel
		}, func(t *testing.T, c *client) error {
			_, err := c.ListCalls(t.Context(), "scope", "run")
			return err
		}},
		{"cursor_close", func(c *client) {
			c.calls.(*fakeCollection).closeErr = sentinel
		}, func(t *testing.T, c *client) error {
			_, err := c.ListCalls(t.Context(), "scope", "run")
			return err
		}},
		{"delete_content", func(c *client) {
			c.content.(*fakeCollection).deleteErr = sentinel
		}, deleteScope},
		{"delete_calls", func(c *client) {
			c.calls.(*fakeCollection).deleteErr = sentinel
		}, deleteScope},
		{"delete_counters", func(c *client) {
			c.counters.(*fakeCollection).deleteErr = sentinel
		}, deleteScope},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newFakeClient(t)
			tc.setup(c)
			assert.ErrorIs(t, tc.run(t, c), sentinel)
		})
	}
}

func TestIndexErrors(t *testing.T) {
	sentinel := errors.New("index unavailable")
	for _, failed := range []string{"content", "calls", "counters"} {
		t.Run(failed, func(t *testing.T) {
			content, calls, counters := &fakeCollection{t: t}, &fakeCollection{t: t}, &fakeCollection{t: t}
			map[string]*fakeCollection{"content": content, "calls": calls, "counters": counters}[failed].indexErr = sentinel
			assert.ErrorIs(t, ensureIndexes(t.Context(), content, calls, counters), sentinel)
		})
	}
}

func TestPutStoresOnce(t *testing.T) {
	c := newFakeClient(t)
	for range 2 {
		_, err := c.Put(t.Context(), "scope", []byte("body"))
		require.NoError(t, err)
	}
	assert.Len(t, c.content.(*fakeCollection).docs, 1)
}

func newFakeClient(t *testing.T) *client {
	t.Helper()
	content, calls, counters := &fakeCollection{t: t}, &fakeCollection{t: t}, &fakeCollection{t: t}
	require.NoError(t, ensureIndexes(t.Context(), content, calls, counters))
	return &client{content: content, calls: calls, counters: counters, timeout: time.Second}
}

func deleteScope(t *testing.T, c *client) error {
	t.Helper()
	return c.DeleteScope(t.Context(), "scope")
}

func testCall(state runstore.CallState) runstore.CallRecord {
	r := runstore.CallRecord{Key: runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}, State: state, Started: []byte("request")}
	if state != runstore.CallStarted {
		r.Completion = []byte("response")
	}
	return r
}
