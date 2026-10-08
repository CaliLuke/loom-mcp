package mongo

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
)

const (
	testEventKey  = "evt-1"
	testRunID     = "run-1"
	testAgentID   = "agent-1"
	testSessionID = "session-1"
	testTurnID    = "turn-1"
)

func TestClientAppendAssignsID(t *testing.T) {
	t.Parallel()

	oid := mustOID(t)
	coll := &fakeCollection{
		insertedID: oid,
	}
	c := &client{coll: coll}

	e := &runlog.Event{
		EventKey:  testEventKey,
		RunID:     testRunID,
		AgentID:   testAgentID,
		SessionID: testSessionID,
		TurnID:    testTurnID,
		Type:      hooks.RunStarted,
		Payload:   []byte(`{"ok":true}`),
		Timestamp: time.Unix(1, 0).UTC(),
	}
	res, err := c.Append(context.Background(), e)
	require.NoError(t, err)
	require.True(t, res.Inserted)
	assert.Equal(t, oid.Hex(), e.ID)
}

func TestClientWithTimeoutPreservesNilContextPanic(t *testing.T) {
	t.Parallel()

	c := &client{timeout: time.Second}
	var nilCtx context.Context

	assert.Panics(t, func() {
		_, _ = c.withTimeout(nilCtx)
	})
}

func TestClientListNextCursor(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		eventCount int
		limit      int
		wantNext   string
	}
	cases := []testCase{
		{
			name:       "fewer_than_limit",
			eventCount: 2,
			limit:      3,
			wantNext:   "",
		},
		{
			name:       "exactly_limit_no_more",
			eventCount: 3,
			limit:      3,
			wantNext:   "",
		},
		{
			name:       "more_than_limit_has_next",
			eventCount: 4,
			limit:      3,
			wantNext:   "000000000000000000000003",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runID := testRunID
			coll := &fakeCollection{
				findDocs: fakeEventDocuments(runID, tc.eventCount),
			}
			c := &client{coll: coll}

			page, err := c.List(context.Background(), runID, "", tc.limit)
			require.NoError(t, err)
			assert.Len(t, page.Events, min(tc.eventCount, tc.limit))
			assert.Equal(t, tc.wantNext, page.NextCursor)

			if tc.wantNext == "" {
				return
			}

			next, err := c.List(context.Background(), runID, page.NextCursor, tc.limit)
			require.NoError(t, err)
			assert.Len(t, next.Events, tc.eventCount-tc.limit)
			assert.Empty(t, next.NextCursor)
		})
	}
}

func TestClientAppendReturnsExistingIDForDuplicateEventKey(t *testing.T) {
	t.Parallel()

	oid := mustOID(t)
	coll := &fakeCollection{
		insertedID: oid,
	}
	c := &client{coll: coll}

	e := &runlog.Event{
		RunID:     testRunID,
		AgentID:   testAgentID,
		SessionID: testSessionID,
		TurnID:    testTurnID,
		Type:      hooks.RunStarted,
		Payload:   []byte(`{"ok":true}`),
		Timestamp: time.Unix(1, 0).UTC(),
		EventKey:  testEventKey,
	}
	first, err := c.Append(context.Background(), e)
	require.NoError(t, err)
	require.True(t, first.Inserted)

	coll.insertErr = mongodriver.WriteException{
		WriteErrors: []mongodriver.WriteError{
			{Code: 11000, Message: "duplicate key"},
		},
	}
	coll.findOneDoc = eventDocument{
		ID:        oid,
		RunID:     testRunID,
		AgentID:   testAgentID,
		SessionID: testSessionID,
		TurnID:    testTurnID,
		Type:      string(hooks.RunStarted),
		Payload:   []byte(`{"ok":true}`),
		Timestamp: time.Unix(1, 0).UTC(),
		EventKey:  testEventKey,
	}

	dup := &runlog.Event{
		RunID:     testRunID,
		AgentID:   testAgentID,
		SessionID: testSessionID,
		TurnID:    testTurnID,
		Type:      hooks.RunStarted,
		Payload:   []byte(`{"ok":true}`),
		Timestamp: time.Unix(1, 0).UTC(),
		EventKey:  testEventKey,
	}
	second, err := c.Append(context.Background(), dup)
	require.NoError(t, err)
	require.False(t, second.Inserted)
	require.Equal(t, oid.Hex(), second.ID)
	require.Equal(t, oid.Hex(), dup.ID)
}

func TestClientAppendReturnsExistingIDForDuplicateEventKeyWithSubMillisecondTimestamp(t *testing.T) {
	t.Parallel()

	oid := mustOID(t)
	coll := &fakeCollection{
		insertedID: oid,
	}
	c := &client{coll: coll}

	timestamp := time.Unix(1, int64(123*time.Millisecond+456*time.Microsecond+789*time.Nanosecond)).UTC()
	e := &runlog.Event{
		RunID:     testRunID,
		AgentID:   testAgentID,
		SessionID: testSessionID,
		TurnID:    testTurnID,
		Type:      hooks.RunStarted,
		Payload:   []byte(`{"ok":true}`),
		Timestamp: timestamp,
		EventKey:  testEventKey,
	}
	first, err := c.Append(context.Background(), e)
	require.NoError(t, err)
	require.True(t, first.Inserted)

	coll.insertErr = mongodriver.WriteException{
		WriteErrors: []mongodriver.WriteError{
			{Code: 11000, Message: "duplicate key"},
		},
	}
	coll.findOneDoc = eventDocument{
		ID:        oid,
		RunID:     testRunID,
		AgentID:   testAgentID,
		SessionID: testSessionID,
		TurnID:    testTurnID,
		Type:      string(hooks.RunStarted),
		Payload:   []byte(`{"ok":true}`),
		Timestamp: timestamp.UTC().Truncate(time.Millisecond),
		EventKey:  testEventKey,
	}

	dup := &runlog.Event{
		RunID:     testRunID,
		AgentID:   testAgentID,
		SessionID: testSessionID,
		TurnID:    testTurnID,
		Type:      hooks.RunStarted,
		Payload:   []byte(`{"ok":true}`),
		Timestamp: timestamp,
		EventKey:  testEventKey,
	}
	second, err := c.Append(context.Background(), dup)
	require.NoError(t, err)
	require.False(t, second.Inserted)
	require.Equal(t, oid.Hex(), second.ID)
	require.Equal(t, oid.Hex(), dup.ID)
}

func fakeEventDocuments(runID string, n int) []eventDocument {
	docs := make([]eventDocument, 0, n)
	for i := 1; i <= n; i++ {
		oid := bson.ObjectID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, byte(i)}
		docs = append(docs, eventDocument{
			ID:        oid,
			Sequence:  int64(i),
			RunID:     runID,
			AgentID:   testAgentID,
			SessionID: testSessionID,
			TurnID:    testTurnID,
			Type:      string(hooks.RunStarted),
			Payload:   []byte(`{}`),
			Timestamp: time.Unix(int64(i), 0).UTC(),
		})
	}
	return docs
}

func mustOID(t *testing.T) bson.ObjectID {
	t.Helper()

	oid, err := bson.ObjectIDFromHex("000000000000000000000001")
	if !assert.NoError(t, err) {
		t.FailNow()
	}
	return oid
}

type fakeCollection struct {
	mu           sync.Mutex
	insertedID   any
	findDocs     []eventDocument
	findOneDoc   eventDocument
	insertErr    error
	updateErr    error
	beforeInsert func(*fakeCollection, eventDocument) error
	indexView    indexView
}

func (c *fakeCollection) InsertOne(_ context.Context, document any, _ ...options.Lister[options.InsertOneOptions]) (*mongodriver.InsertOneResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if doc, ok := document.(eventDocument); ok && c.beforeInsert != nil {
		if err := c.beforeInsert(c, doc); err != nil {
			return nil, err
		}
	}
	if c.insertErr != nil {
		return nil, c.insertErr
	}
	if doc, ok := document.(eventDocument); ok {
		id, idOK := c.insertedID.(bson.ObjectID)
		if c.insertedID != nil && !idOK {
			return &mongodriver.InsertOneResult{InsertedID: c.insertedID}, nil
		}
		if !idOK {
			id = bson.NewObjectID()
		}
		doc.ID = id
		c.findDocs = append(c.findDocs, doc)
		return &mongodriver.InsertOneResult{InsertedID: id}, nil
	}
	return &mongodriver.InsertOneResult{InsertedID: c.insertedID}, nil
}

func (c *fakeCollection) FindOne(_ context.Context, filter any, opts ...options.Lister[options.FindOneOptions]) singleResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, ok := filter.(bson.M)
	if !ok {
		return fakeSingleResult{err: errors.New("unsupported fake find-one filter")}
	}
	docs := append([]eventDocument(nil), c.findDocs...)
	if !c.findOneDoc.ID.IsZero() && fakeFilterEvent(c.findOneDoc, f) {
		docs = append(docs, c.findOneDoc)
	}
	filtered := make([]eventDocument, 0, len(docs))
	for _, doc := range docs {
		if fakeFilterEvent(doc, f) {
			filtered = append(filtered, doc)
		}
	}
	if len(filtered) == 0 {
		return fakeSingleResult{err: mongodriver.ErrNoDocuments}
	}
	findOpts, err := applyTestOptions[options.FindOneOptions](opts...)
	if err != nil {
		return fakeSingleResult{err: err}
	}
	if findOpts.Sort != nil {
		sort.Slice(filtered, func(i, j int) bool {
			return fakeSortLess(filtered[i], filtered[j], findOpts.Sort)
		})
	}
	return fakeSingleResult{doc: filtered[0]}
}

func (c *fakeCollection) Find(_ context.Context, filter any, opts ...options.Lister[options.FindOptions]) (cursor, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f, ok := filter.(bson.M)
	if !ok {
		return &fakeCursor{}, nil
	}

	runID, _ := f["run_id"].(string)
	var after int64
	if id, ok := f[fieldSequence].(bson.M); ok {
		if gt, ok := id["$gt"].(int64); ok {
			after = gt
		}
	}

	filtered := make([]eventDocument, 0, len(c.findDocs))
	for _, doc := range c.findDocs {
		if doc.RunID != runID {
			continue
		}
		if doc.Sequence <= after {
			continue
		}
		filtered = append(filtered, doc)
	}

	findOpts, err := applyTestOptions[options.FindOptions](opts...)
	if err != nil {
		return nil, err
	}
	var limit int64
	if findOpts.Limit != nil {
		limit = *findOpts.Limit
	}
	if findOpts.Sort != nil {
		sort.Slice(filtered, func(i, j int) bool {
			return fakeSortLess(filtered[i], filtered[j], findOpts.Sort)
		})
	}
	if limit > 0 && int64(len(filtered)) > limit {
		filtered = filtered[:limit]
	}

	return &fakeCursor{docs: filtered}, nil
}

func (c *fakeCollection) UpdateOne(_ context.Context, filter any, update any, _ ...options.Lister[options.UpdateOneOptions]) (*mongodriver.UpdateResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.updateErr != nil {
		return nil, c.updateErr
	}
	f, ok := filter.(bson.M)
	if !ok {
		return nil, errors.New("unsupported fake update filter")
	}
	u, ok := update.(bson.M)
	if !ok {
		return nil, errors.New("unsupported fake update document")
	}
	set, ok := u["$set"].(bson.M)
	if !ok {
		return nil, errors.New("unsupported fake update operator")
	}
	for i, doc := range c.findDocs {
		if fakeFilterEvent(doc, f) {
			if sequence, ok := set[fieldSequence].(int64); ok {
				doc.Sequence = sequence
				c.findDocs[i] = doc
			}
			return &mongodriver.UpdateResult{MatchedCount: 1, ModifiedCount: 1}, nil
		}
	}
	return &mongodriver.UpdateResult{}, nil
}

func fakeFilterEvent(doc eventDocument, filter bson.M) bool {
	for key, want := range filter {
		switch key {
		case fieldRunID:
			if doc.RunID != want {
				return false
			}
		case fieldEventKey:
			if doc.EventKey != want {
				return false
			}
		case fieldID:
			if doc.ID != want {
				return false
			}
		case fieldSequence:
			switch condition := want.(type) {
			case int64:
				if doc.Sequence != condition {
					return false
				}
			case bson.M:
				if exists, ok := condition[operatorExists].(bool); ok && exists != (doc.Sequence != 0) {
					return false
				}
				if gt, ok := condition["$gt"].(int64); ok && doc.Sequence <= gt {
					return false
				}
			}
		}
	}
	return true
}

func fakeSortLess(left, right eventDocument, sort any) bool {
	keys, ok := sort.(bson.D)
	if !ok {
		return false
	}
	for _, key := range keys {
		order := 0
		switch value := key.Value.(type) {
		case int:
			order = value
		case int32:
			order = int(value)
		}
		var comparison int
		switch key.Key {
		case fieldSequence:
			if left.Sequence < right.Sequence {
				comparison = -1
			} else if left.Sequence > right.Sequence {
				comparison = 1
			}
		case fieldRunID:
			comparison = strings.Compare(left.RunID, right.RunID)
		case fieldID:
			comparison = bytes.Compare(left.ID[:], right.ID[:])
		}
		if comparison != 0 {
			return comparison*order < 0
		}
	}
	return false
}

func (c *fakeCollection) Indexes() indexView {
	if c.indexView != nil {
		return c.indexView
	}
	return fakeIndexView{}
}

type fakeIndexView struct{}

func (fakeIndexView) CreateOne(context.Context, mongodriver.IndexModel, ...options.Lister[options.CreateIndexesOptions]) (string, error) {
	return "", nil
}

func applyTestOptions[T any](opts ...options.Lister[T]) (*T, error) {
	var out T
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		for _, set := range opt.List() {
			if err := set(&out); err != nil {
				return nil, err
			}
		}
	}
	return &out, nil
}

type fakeCursor struct {
	docs []eventDocument
	pos  int
	err  error
}

func (c *fakeCursor) Next(context.Context) bool {
	if c.err != nil {
		return false
	}
	if c.pos >= len(c.docs) {
		return false
	}
	c.pos++
	return true
}

func (c *fakeCursor) Decode(val any) error {
	if c.err != nil {
		return c.err
	}
	if c.pos == 0 || c.pos > len(c.docs) {
		return nil
	}
	p, ok := val.(*eventDocument)
	if !ok {
		return nil
	}
	*p = c.docs[c.pos-1]
	return nil
}

func (c *fakeCursor) Err() error {
	return c.err
}

func (c *fakeCursor) Close(context.Context) error {
	return nil
}

type fakeSingleResult struct {
	doc eventDocument
	err error
}

func (r fakeSingleResult) Decode(val any) error {
	if r.err != nil {
		return r.err
	}
	p, ok := val.(*eventDocument)
	if !ok {
		return nil
	}
	*p = r.doc
	return nil
}
