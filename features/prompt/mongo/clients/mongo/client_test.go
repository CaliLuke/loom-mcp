package mongo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/CaliLuke/loom-mcp/runtime/agent/prompt"
)

const (
	labelAccount     = "account"
	labelRegion      = "region"
	accountAcme      = "acme"
	regionWest       = "west"
	missingSessionID = "missing_session"
)

func TestEnsureIndexes(t *testing.T) {
	t.Parallel()

	fc := newFakeCollection()
	err := ensureIndexes(context.Background(), fc)
	require.NoError(t, err)
	require.Len(t, fc.indexes, 2)
}

func TestSetAndResolveByPrecedence(t *testing.T) {
	t.Parallel()

	client := mustNewTestClient()
	ctx := context.Background()
	id := prompt.Ident("example.agent.system")

	require.NoError(t, client.Set(ctx, id, prompt.Scope{}, "global", nil))
	require.NoError(t, client.Set(ctx, id, prompt.Scope{
		Labels: map[string]string{
			labelAccount: accountAcme,
		},
	}, "account", nil))
	require.NoError(t, client.Set(ctx, id, prompt.Scope{
		Labels: map[string]string{
			labelAccount: accountAcme,
			labelRegion:  regionWest,
		},
	}, "region", nil))
	require.NoError(t, client.Set(ctx, id, prompt.Scope{
		SessionID: "sess_1",
		Labels: map[string]string{
			labelAccount: accountAcme,
			labelRegion:  regionWest,
		},
	}, "session", nil))

	override, err := client.Resolve(ctx, id, prompt.Scope{
		SessionID: "sess_1",
		Labels: map[string]string{
			labelAccount: accountAcme,
			labelRegion:  regionWest,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, override)
	require.Equal(t, "session", override.Template)
}

func TestResolveFallsBackAcrossScopes(t *testing.T) {
	t.Parallel()

	client := mustNewTestClient()
	ctx := context.Background()
	id := prompt.Ident("example.agent.system")
	require.NoError(t, client.Set(ctx, id, prompt.Scope{}, "global", nil))
	require.NoError(t, client.Set(ctx, id, prompt.Scope{
		Labels: map[string]string{
			labelAccount: accountAcme,
		},
	}, "account", nil))
	require.NoError(t, client.Set(ctx, id, prompt.Scope{
		Labels: map[string]string{
			labelAccount: accountAcme,
			labelRegion:  regionWest,
		},
	}, "region", nil))

	override, err := client.Resolve(ctx, id, prompt.Scope{
		SessionID: missingSessionID,
		Labels: map[string]string{
			labelAccount: accountAcme,
			labelRegion:  regionWest,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, override)
	require.Equal(t, "region", override.Template)

	override, err = client.Resolve(ctx, id, prompt.Scope{
		SessionID: missingSessionID,
		Labels: map[string]string{
			labelAccount: accountAcme,
			labelRegion:  "missing_region",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, override)
	require.Equal(t, "account", override.Template)

	override, err = client.Resolve(ctx, id, prompt.Scope{
		SessionID: missingSessionID,
		Labels: map[string]string{
			labelAccount: "missing_account",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, override)
	require.Equal(t, "global", override.Template)
}

func TestResolveReturnsNilWhenMissing(t *testing.T) {
	t.Parallel()

	client := mustNewTestClient()
	override, err := client.Resolve(context.Background(), "missing", prompt.Scope{})
	require.NoError(t, err)
	require.Nil(t, override)
}

func TestResolveQueriesGrowLinearlyWithLabels(t *testing.T) {
	t.Parallel()

	labels := make(map[string]string)
	for i := range 32 {
		labels[fmt.Sprintf("label_%02d", i)] = "value"
	}
	queries := resolveQueries("example.agent.system", prompt.Scope{
		SessionID: "sess_1",
		Labels:    labels,
	})

	require.Len(t, queries, (len(labels)+1)*2)
}

func TestResolveUsesQueryPrecedenceWithLimit(t *testing.T) {
	t.Parallel()

	client := mustNewTestClient()
	fc := client.coll.(*fakeCollection)
	ctx := context.Background()
	id := prompt.Ident("example.agent.system")
	fc.docs = []overrideDocument{
		{PromptID: id.String(), Template: "newer-global", CreatedAt: time.Unix(3, 0).UTC(), ScopeLabelCount: 0},
		{PromptID: id.String(), ScopeSession: "sess_1", Template: "older-session", CreatedAt: time.Unix(1, 0).UTC(), ScopeLabelCount: 0},
		{PromptID: id.String(), ScopeSession: "sess_1", Template: "newer-session", CreatedAt: time.Unix(2, 0).UTC(), ScopeLabelCount: 0},
	}

	override, err := client.Resolve(ctx, id, prompt.Scope{
		SessionID: "sess_1",
	})

	require.NoError(t, err)
	require.NotNil(t, override)
	require.Equal(t, "newer-session", override.Template)
	require.Equal(t, []int{1}, fc.findLimits())
}

func TestResolveSupportsMongoPathUnsafeLabelKeys(t *testing.T) {
	t.Parallel()

	client := mustNewTestClient()
	fc := client.coll.(*fakeCollection)
	ctx := context.Background()
	id := prompt.Ident("example.agent.system")
	fc.docs = []overrideDocument{
		{PromptID: id.String(), Template: "global", CreatedAt: time.Unix(2, 0).UTC(), ScopeLabelCount: 0},
		{PromptID: id.String(), Template: "unsafe-label", CreatedAt: time.Unix(1, 0).UTC(), ScopeLabels: map[string]string{"env.name": "prod"}, ScopeLabelCount: 1},
	}

	override, err := client.Resolve(ctx, id, prompt.Scope{
		Labels: map[string]string{"env.name": "prod"},
	})

	require.NoError(t, err)
	require.NotNil(t, override)
	require.Equal(t, "unsafe-label", override.Template)
	require.Equal(t, []int{0}, fc.findLimits())
}

func TestHistoryAndListNewestFirst(t *testing.T) {
	t.Parallel()

	client := mustNewTestClient()
	fc := client.coll.(*fakeCollection)
	ctx := context.Background()
	id := prompt.Ident("example.agent.system")
	require.NoError(t, client.Set(ctx, id, prompt.Scope{}, "first", nil))
	fc.docs[0].CreatedAt = time.Unix(1, 0).UTC()
	require.NoError(t, client.Set(ctx, id, prompt.Scope{}, "second", nil))
	fc.docs[1].CreatedAt = time.Unix(2, 0).UTC()

	history, err := client.History(ctx, id)
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.Equal(t, "second", history[0].Template)
	require.Equal(t, "first", history[1].Template)

	list, err := client.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, "second", list[0].Template)
}

func TestSetValidation(t *testing.T) {
	t.Parallel()

	client := mustNewTestClient()
	ctx := context.Background()
	err := client.Set(ctx, "", prompt.Scope{}, "template", nil)
	require.EqualError(t, err, "prompt id is required")
	err = client.Set(ctx, "example.agent.system", prompt.Scope{}, "", nil)
	require.EqualError(t, err, "template is required")
}

func TestSetWritesDocumentDefaults(t *testing.T) {
	t.Parallel()

	client := mustNewTestClient()
	ctx := context.Background()
	template := "hello {{ .Name }}"
	err := client.Set(ctx, "example.agent.system", prompt.Scope{}, template, nil)
	require.NoError(t, err)

	fc := client.coll.(*fakeCollection)
	require.Len(t, fc.docs, 1)
	doc := fc.docs[0]
	require.Equal(t, "example.agent.system", doc.PromptID)
	require.Empty(t, doc.ScopeSession)
	require.Nil(t, doc.ScopeLabels)
	require.Equal(t, 0, doc.ScopeLabelCount)
	require.Equal(t, prompt.VersionFromTemplate(template), doc.Version)
	require.False(t, doc.CreatedAt.IsZero())
	require.Nil(t, doc.Metadata)
}

func mustNewTestClient() *client {
	fc := newFakeCollection()
	c, err := newClientWithCollection(nil, fc, time.Second)
	if err != nil {
		panic(err)
	}
	return c
}

type fakeCollection struct {
	mu          sync.Mutex
	indexes     []mongodriver.IndexModel
	docs        []overrideDocument
	findOptions []options.FindOptions
}

func newFakeCollection() *fakeCollection {
	return &fakeCollection{
		docs: make([]overrideDocument, 0),
	}
}

func (c *fakeCollection) FindOne(ctx context.Context, filter any, opts ...options.Lister[options.FindOneOptions]) singleResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	matches := c.match(filter)
	if len(matches) == 0 {
		return fakeSingleResult{err: mongodriver.ErrNoDocuments}
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})
	return fakeSingleResult{doc: &matches[0]}
}

func (c *fakeCollection) Find(ctx context.Context, filter any, opts ...options.Lister[options.FindOptions]) (cursor, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	matches := c.match(filter)
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].CreatedAt.After(matches[j].CreatedAt)
	})
	findOpts, err := mergeFindOptions(opts...)
	if err != nil {
		return nil, err
	}
	c.findOptions = append(c.findOptions, findOpts)
	if findOpts.Limit != nil && *findOpts.Limit >= 0 && int(*findOpts.Limit) < len(matches) {
		matches = matches[:int(*findOpts.Limit)]
	}
	return &fakeCursor{docs: matches, idx: -1}, nil
}

func (c *fakeCollection) findLimits() []int {
	c.mu.Lock()
	defer c.mu.Unlock()

	limits := make([]int, 0, len(c.findOptions))
	for _, opts := range c.findOptions {
		if opts.Limit == nil {
			limits = append(limits, 0)
			continue
		}
		limits = append(limits, int(*opts.Limit))
	}
	return limits
}

func mergeFindOptions(opts ...options.Lister[options.FindOptions]) (options.FindOptions, error) {
	var out options.FindOptions
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		for _, set := range opt.List() {
			if err := set(&out); err != nil {
				return options.FindOptions{}, err
			}
		}
	}
	return out, nil
}

func (c *fakeCollection) InsertOne(ctx context.Context, document any, opts ...options.Lister[options.InsertOneOptions]) (*mongodriver.InsertOneResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	doc, ok := document.(overrideDocument)
	if !ok {
		return nil, errors.New("unexpected insert document type")
	}
	c.docs = append(c.docs, doc)
	return &mongodriver.InsertOneResult{InsertedID: "id"}, nil
}

func (c *fakeCollection) Indexes() indexView {
	return fakeIndexView{parent: c}
}

func (c *fakeCollection) match(filter any) []overrideDocument {
	f, _ := filter.(bson.M)
	out := make([]overrideDocument, 0)
	for _, doc := range c.docs {
		if !matchesFilter(doc, f) {
			continue
		}
		out = append(out, doc)
	}
	return out
}

func matchesFilter(doc overrideDocument, filter bson.M) bool {
	for key, val := range filter {
		switch key {
		case "$or":
			if !matchesAnyFilter(doc, val) {
				return false
			}
		case fieldPromptID:
			if doc.PromptID != val {
				return false
			}
		case fieldScopeSession:
			if !matchesStringCondition(doc.ScopeSession, val) {
				return false
			}
		case fieldScopeLabelCount:
			if doc.ScopeLabelCount != val {
				return false
			}
		default:
			if !matchesDottedLabel(doc, key, val) {
				return false
			}
		}
	}
	return true
}

func matchesAnyFilter(doc overrideDocument, val any) bool {
	switch filters := val.(type) {
	case []bson.M:
		for _, filter := range filters {
			if matchesFilter(doc, filter) {
				return true
			}
		}
	case bson.A:
		for _, raw := range filters {
			filter, ok := raw.(bson.M)
			if !ok {
				continue
			}
			if matchesFilter(doc, filter) {
				return true
			}
		}
	}
	return false
}

func matchesStringCondition(got string, condition any) bool {
	switch cond := condition.(type) {
	case string:
		return got == cond
	case bson.M:
		in, ok := cond["$in"]
		if !ok {
			return false
		}
		values, ok := in.([]string)
		if !ok {
			return false
		}
		for _, value := range values {
			if got == value {
				return true
			}
		}
	}
	return false
}

func matchesDottedLabel(doc overrideDocument, key string, val any) bool {
	const prefix = fieldScopeLabels + "."
	if len(key) <= len(prefix) || key[:len(prefix)] != prefix {
		return false
	}
	label := key[len(prefix):]
	return doc.ScopeLabels[label] == val
}

type fakeSingleResult struct {
	doc *overrideDocument
	err error
}

func (r fakeSingleResult) Decode(val any) error {
	if r.err != nil {
		return r.err
	}
	out, ok := val.(*overrideDocument)
	if !ok {
		return errors.New("unexpected decode target")
	}
	*out = *r.doc
	return nil
}

type fakeCursor struct {
	docs []overrideDocument
	idx  int
}

func (c *fakeCursor) Close(ctx context.Context) error {
	return nil
}

func (c *fakeCursor) Decode(val any) error {
	if c.idx < 0 || c.idx >= len(c.docs) {
		return errors.New("no current document")
	}
	out, ok := val.(*overrideDocument)
	if !ok {
		return errors.New("unexpected decode target")
	}
	*out = c.docs[c.idx]
	return nil
}

func (c *fakeCursor) Err() error {
	return nil
}

func (c *fakeCursor) Next(ctx context.Context) bool {
	next := c.idx + 1
	if next >= len(c.docs) {
		return false
	}
	c.idx = next
	return true
}

type fakeIndexView struct {
	parent *fakeCollection
}

func (v fakeIndexView) CreateOne(ctx context.Context, model mongodriver.IndexModel,
	opts ...options.Lister[options.CreateIndexesOptions]) (string, error) {
	v.parent.mu.Lock()
	defer v.parent.mu.Unlock()
	v.parent.indexes = append(v.parent.indexes, model)
	return "idx", nil
}
