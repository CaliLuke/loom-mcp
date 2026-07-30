package mongo

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/memory"
)

func TestEnsureIndexes(t *testing.T) {
	legacy := newFakeCollection()
	buckets := newFakeBucketCollection()
	require.NoError(t, ensureLegacyIndexes(context.Background(), legacy))
	require.NoError(t, ensureEventIndexes(context.Background(), buckets))
	require.True(t, legacy.indexCreated)
	require.True(t, buckets.indexCreated)
	require.Equal(t, bson.D{
		{Key: fieldAgentID, Value: 1},
		{Key: fieldRunID, Value: 1},
		{Key: fieldCreatedAt, Value: 1},
		{Key: fieldID, Value: 1},
	}, buckets.indexKeys)
}

func TestResolveEventsCollectionFollowsLegacyOverride(t *testing.T) {
	require.Equal(t, defaultEventsCollection, resolveEventsCollection("", ""))
	require.Equal(t, "tenant_memory_events", resolveEventsCollection("tenant_memory", ""))
	require.Equal(t, "explicit_events", resolveEventsCollection("tenant_memory", "explicit_events"))
}

func TestNewRejectsSharedLegacyAndEventsCollection(t *testing.T) {
	_, err := New(Options{Client: &mongodriver.Client{}, Database: "db", Collection: "memory", EventsCollection: "memory"})
	require.EqualError(t, err, "events collection must differ from legacy collection")
}

func TestLoadRunMissingReturnsEmptySnapshot(t *testing.T) {
	client := mustNewTestClient()
	snap, err := client.LoadRun(context.Background(), "agent", "run")
	require.NoError(t, err)
	require.Equal(t, "agent", snap.AgentID)
	require.Equal(t, "run", snap.RunID)
	require.Empty(t, snap.Events)
	require.NotNil(t, snap.Meta)
}

func TestAppendAndLoadRun(t *testing.T) {
	client := mustNewTestClient()
	ts := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	err := client.AppendEvents(context.Background(), "agent", "run", []memory.Event{
		{
			Type:      memory.EventToolCall,
			Timestamp: ts,
			Data:      map[string]any{"name": "foo"},
			Labels:    map[string]string{"kind": "call"},
		},
		{
			Type:   memory.EventAssistantMessage,
			Data:   map[string]any{"text": "done"},
			Labels: map[string]string{"kind": "assistant"},
		},
	})
	require.NoError(t, err)

	snap, err := client.LoadRun(context.Background(), "agent", "run")
	require.NoError(t, err)
	require.Len(t, snap.Events, 2)
	require.Equal(t, memory.EventToolCall, snap.Events[0].Type)
	require.Equal(t, ts, snap.Events[0].Timestamp)
	require.Equal(t, "foo", snap.Events[0].Data.(map[string]any)["name"])
	require.Equal(t, "call", snap.Events[0].Labels["kind"])
	require.Equal(t, memory.EventAssistantMessage, snap.Events[1].Type)
	require.NotZero(t, snap.Events[1].Timestamp)
	require.Equal(t, "assistant", snap.Events[1].Labels["kind"])
}

func TestLoadRunMergesLegacyDocumentWithAppendBuckets(t *testing.T) {
	legacy := newFakeCollection()
	buckets := newFakeBucketCollection()
	client, err := newClientWithCollections(nil, legacy, buckets, time.Second)
	require.NoError(t, err)
	legacy.docs["agent|run"] = &runDocument{
		AgentID: "agent",
		RunID:   "run",
		Events: []eventDocument{{
			Type:      memory.EventUserMessage,
			Timestamp: time.Unix(1, 0).UTC(),
			Data:      map[string]any{"text": "legacy"},
		}},
		Meta: map[string]any{"source": "legacy"},
	}

	require.NoError(t, client.AppendEvents(context.Background(), "agent", "run", []memory.Event{{
		Type:      memory.EventAssistantMessage,
		Timestamp: time.Unix(2, 0).UTC(),
		Data:      map[string]any{"text": "bucket"},
	}}))

	snap, err := client.LoadRun(context.Background(), "agent", "run")
	require.NoError(t, err)
	require.Len(t, snap.Events, 2)
	require.Equal(t, memory.EventUserMessage, snap.Events[0].Type)
	require.Equal(t, memory.EventAssistantMessage, snap.Events[1].Type)
	require.Equal(t, "legacy", snap.Events[0].Data.(map[string]any)["text"])
	require.Equal(t, "bucket", snap.Events[1].Data.(map[string]any)["text"])
	require.Equal(t, "legacy", snap.Meta["source"])
	require.Empty(t, legacy.updates, "new events must not grow the legacy run document")
	require.Len(t, buckets.docs, 1)
}

func TestLoadRunOrdersLegacyAndBucketsChronologically(t *testing.T) {
	legacy := newFakeCollection()
	buckets := newFakeBucketCollection()
	client, err := newClientWithCollections(nil, legacy, buckets, time.Second)
	require.NoError(t, err)

	equalTimestamp := time.Unix(20, 0).UTC()
	legacy.docs["agent|run"] = &runDocument{
		AgentID: "agent",
		RunID:   "run",
		Events: []eventDocument{{
			Type:      memory.EventUserMessage,
			Timestamp: equalTimestamp,
			Data:      map[string]any{"text": "legacy-equal"},
		}},
	}
	lowID, err := bson.ObjectIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	highID, err := bson.ObjectIDFromHex("ffffffffffffffffffffffff")
	require.NoError(t, err)
	buckets.docs = []eventBucketDocument{
		{
			ID:        lowID,
			AgentID:   "agent",
			RunID:     "run",
			CreatedAt: time.Unix(40, 0).UTC(),
			Events: []eventDocument{{
				Type:      memory.EventAssistantMessage,
				Timestamp: equalTimestamp,
				Data:      map[string]any{"text": "newer-bucket"},
			}},
		},
		{
			ID:        highID,
			AgentID:   "agent",
			RunID:     "run",
			CreatedAt: time.Unix(30, 0).UTC(),
			Events: []eventDocument{
				{
					Type:      memory.EventToolCall,
					Timestamp: time.Unix(10, 0).UTC(),
					Data:      map[string]any{"text": "chronologically-first"},
				},
				{
					Type:      memory.EventAssistantMessage,
					Timestamp: equalTimestamp,
					Data:      map[string]any{"text": "older-bucket"},
				},
			},
		},
	}

	snapshot, err := client.LoadRun(context.Background(), "agent", "run")
	require.NoError(t, err)
	require.Len(t, snapshot.Events, 4)
	require.Equal(t, []string{
		"chronologically-first",
		"legacy-equal",
		"older-bucket",
		"newer-bucket",
	}, eventTexts(snapshot.Events))
}

func TestLoadRunNormalizesBSONEventDataForMemoryDecoders(t *testing.T) {
	fc := newFakeCollection()
	client, err := newClientWithCollections(nil, fc, newFakeBucketCollection(), time.Second)
	require.NoError(t, err)

	ts := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)
	fc.docs["agent|run"] = &runDocument{
		AgentID: "agent",
		RunID:   "run",
		Events: []eventDocument{
			{
				Type:      memory.EventToolCall,
				Timestamp: ts,
				Data: bson.D{
					{Key: "tool_call_id", Value: "tc-1"},
					{Key: "tool_name", Value: "svc.tool"},
					{Key: "payload", Value: bson.D{
						{Key: "query", Value: "memory"},
						{Key: "filters", Value: bson.A{
							bson.D{{Key: "field", Value: "tenant"}, {Key: "value", Value: "acme"}},
						}},
					}},
				},
			},
		},
	}

	snap, err := client.LoadRun(context.Background(), "agent", "run")
	require.NoError(t, err)
	require.Len(t, snap.Events, 1)
	require.IsType(t, map[string]any{}, snap.Events[0].Data)

	decoded, err := memory.DecodeToolCallData(snap.Events[0])
	require.NoError(t, err)
	input, err := decoded.Input()
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"query": "memory",
		"filters": []any{
			map[string]any{"field": "tenant", "value": "acme"},
		},
	}, input)
}

func TestAppendEventsRequiresIdentifiers(t *testing.T) {
	client := mustNewTestClient()
	err := client.AppendEvents(context.Background(), "", "run", []memory.Event{{Type: memory.EventPlannerNote}})
	require.EqualError(t, err, "agent id is required")
	err = client.AppendEvents(context.Background(), "agent", "", []memory.Event{{Type: memory.EventPlannerNote}})
	require.EqualError(t, err, "run id is required")
}

func TestLoadRunRequiresIdentifiers(t *testing.T) {
	client := mustNewTestClient()
	_, err := client.LoadRun(context.Background(), "", "run")
	require.EqualError(t, err, "agent id is required")
	_, err = client.LoadRun(context.Background(), "agent", "")
	require.EqualError(t, err, "run id is required")
}

func mustNewTestClient() *client {
	cl, err := newClientWithCollections(nil, newFakeCollection(), newFakeBucketCollection(), time.Second)
	if err != nil {
		panic(err)
	}
	return cl
}

func eventTexts(events []memory.Event) []string {
	texts := make([]string, 0, len(events))
	for _, event := range events {
		data, _ := event.Data.(map[string]any)
		text, _ := data["text"].(string)
		texts = append(texts, text)
	}
	return texts
}

// fakeCollection is a lightweight in-memory collection that mimics the subset
// of MongoDB behavior exercised by the client.
type fakeCollection struct {
	mu           sync.Mutex
	indexCreated bool
	docs         map[string]*runDocument
	updates      []any
}

func newFakeCollection() *fakeCollection {
	return &fakeCollection{docs: make(map[string]*runDocument)}
}

func (c *fakeCollection) FindOne(ctx context.Context, filter any, opts ...options.Lister[options.FindOneOptions]) singleResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := docKey(filter)
	doc, ok := c.docs[key]
	if !ok {
		return fakeSingleResult{err: mongodriver.ErrNoDocuments}
	}
	clone := *doc
	clone.Events = append([]eventDocument(nil), doc.Events...)
	return fakeSingleResult{doc: &clone}
}

func (c *fakeCollection) UpdateOne(_ context.Context, filter any, update any,
	_ ...options.Lister[options.UpdateOneOptions]) (*mongodriver.UpdateResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = append(c.updates, update)
	key := docKey(filter)
	doc, ok := c.docs[key]
	if !ok {
		doc = &runDocument{}
		c.docs[key] = doc
	}
	up, _ := update.(bson.M)
	if soi, ok := up["$setOnInsert"].(bson.M); ok && doc.AgentID == "" && doc.RunID == "" {
		if v, ok := soi["agent_id"].(string); ok {
			doc.AgentID = v
		}
		if v, ok := soi["run_id"].(string); ok {
			doc.RunID = v
		}
	}
	if set, ok := up["$set"].(bson.M); ok {
		if v, ok := set["updated_at"].(time.Time); ok {
			doc.UpdatedAt = v
		}
	}
	if push, ok := up["$push"].(bson.M); ok {
		if ev, ok := push["events"].(bson.M); ok {
			if each, ok := ev["$each"].([]eventDocument); ok {
				doc.Events = append(doc.Events, cloneEventDocs(each)...)
			}
		}
	}
	return &mongodriver.UpdateResult{MatchedCount: 1}, nil
}

type fakeBucketCollection struct {
	mu           sync.Mutex
	indexCreated bool
	indexKeys    bson.D
	docs         []eventBucketDocument
}

func newFakeBucketCollection() *fakeBucketCollection {
	return &fakeBucketCollection{}
}

func (c *fakeBucketCollection) InsertOne(_ context.Context, document any, _ ...options.Lister[options.InsertOneOptions]) (*mongodriver.InsertOneResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	doc, ok := document.(eventBucketDocument)
	if !ok {
		return nil, errors.New("unsupported bucket document")
	}
	if doc.ID.IsZero() {
		doc.ID = bson.NewObjectID()
	}
	doc.Events = cloneEventDocs(doc.Events)
	c.docs = append(c.docs, doc)
	return &mongodriver.InsertOneResult{InsertedID: doc.ID}, nil
}

func (c *fakeBucketCollection) Find(_ context.Context, filter any, opts ...options.Lister[options.FindOptions]) (cursor, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	bsonFilter, _ := filter.(bson.M)
	agentID, _ := bsonFilter[fieldAgentID].(string)
	runID, _ := bsonFilter[fieldRunID].(string)
	var docs []eventBucketDocument
	for _, doc := range c.docs {
		if doc.AgentID == agentID && doc.RunID == runID {
			doc.Events = cloneEventDocs(doc.Events)
			docs = append(docs, doc)
		}
	}
	findOptions, err := mergeBucketFindOptions(opts...)
	if err != nil {
		return nil, err
	}
	if fields, ok := findOptions.Sort.(bson.D); ok {
		sortBucketDocuments(docs, fields)
	}
	return &fakeBucketCursor{docs: docs}, nil
}

func mergeBucketFindOptions(opts ...options.Lister[options.FindOptions]) (options.FindOptions, error) {
	var merged options.FindOptions
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		for _, apply := range opt.List() {
			if err := apply(&merged); err != nil {
				return options.FindOptions{}, err
			}
		}
	}
	return merged, nil
}

func sortBucketDocuments(docs []eventBucketDocument, fields bson.D) {
	sort.SliceStable(docs, func(i, j int) bool {
		for _, field := range fields {
			direction, _ := field.Value.(int)
			var comparison int
			switch field.Key {
			case fieldCreatedAt:
				comparison = docs[i].CreatedAt.Compare(docs[j].CreatedAt)
			case fieldID:
				comparison = strings.Compare(docs[i].ID.Hex(), docs[j].ID.Hex())
			}
			if comparison == 0 {
				continue
			}
			if direction < 0 {
				return comparison > 0
			}
			return comparison < 0
		}
		return false
	})
}

func (c *fakeBucketCollection) Indexes() indexView {
	return fakeBucketIndexView{parent: c}
}

type fakeBucketIndexView struct {
	parent *fakeBucketCollection
}

func (v fakeBucketIndexView) CreateOne(_ context.Context, model mongodriver.IndexModel, _ ...options.Lister[options.CreateIndexesOptions]) (string, error) {
	if len(model.Keys.(bson.D)) == 0 {
		return "", errors.New("missing keys")
	}
	v.parent.mu.Lock()
	v.parent.indexCreated = true
	v.parent.indexKeys = append(bson.D(nil), model.Keys.(bson.D)...)
	v.parent.mu.Unlock()
	return "idx_agent_run_id", nil
}

type fakeBucketCursor struct {
	docs  []eventBucketDocument
	index int
}

func (c *fakeBucketCursor) Next(context.Context) bool {
	return c.index < len(c.docs)
}

func (c *fakeBucketCursor) Decode(value any) error {
	dest, ok := value.(*eventBucketDocument)
	if !ok {
		return errors.New("unsupported bucket decode target")
	}
	*dest = c.docs[c.index]
	c.index++
	return nil
}

func (c *fakeBucketCursor) Err() error {
	return nil
}

func (c *fakeBucketCursor) Close(context.Context) error {
	return nil
}

func (c *fakeCollection) Indexes() indexView {
	return fakeIndexView{parent: c}
}

type fakeIndexView struct {
	parent *fakeCollection
}

func (v fakeIndexView) CreateOne(ctx context.Context, model mongodriver.IndexModel,
	opts ...options.Lister[options.CreateIndexesOptions]) (string, error) {
	if len(model.Keys.(bson.D)) == 0 {
		return "", errors.New("missing keys")
	}
	v.parent.mu.Lock()
	v.parent.indexCreated = true
	v.parent.mu.Unlock()
	return "idx_agent_run", nil
}

type fakeSingleResult struct {
	doc *runDocument
	err error
}

func (r fakeSingleResult) Decode(val any) error {
	if r.err != nil {
		return r.err
	}
	dest, ok := val.(*runDocument)
	if !ok {
		return errors.New("unsupported decode target")
	}
	*dest = *r.doc
	return nil
}

func docKey(filter any) string {
	bsonFilter, _ := filter.(bson.M)
	agent, _ := bsonFilter["agent_id"].(string)
	run, _ := bsonFilter["run_id"].(string)
	return agent + "|" + run
}

func cloneEventDocs(src []eventDocument) []eventDocument {
	if len(src) == 0 {
		return nil
	}
	dst := make([]eventDocument, len(src))
	for i, evt := range src {
		evt.Labels = cloneStringMap(evt.Labels)
		dst[i] = evt
	}
	return dst
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
