package mongo

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/CaliLuke/loom-mcp/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/runtime/agent/session"
)

const (
	testAgentChat  = "agent.chat"
	testSessionID  = "sess-1"
	testRunTwo     = "run-2"
	testRunThree   = "run-3"
	testChildRun   = "run-child"
	testChildAgent = "agent.child"
)

func TestEnsureIndexes(t *testing.T) {
	sessions := newFakeSessionsCollection()
	runs := newFakeRunsCollection()
	err := ensureIndexes(context.Background(), sessions, runs)
	require.NoError(t, err)
	require.Equal(t, 1, sessions.indexCreated)
	require.Equal(t, 3, runs.indexCreated)
}

func TestCreateLoadEndSession(t *testing.T) {
	client := mustNewTestClient()
	now := time.Now().UTC()
	sess, err := client.CreateSession(context.Background(), "sess-1", now)
	require.NoError(t, err)
	require.Equal(t, "sess-1", sess.ID)
	require.Equal(t, session.StatusActive, sess.Status)
	require.True(t, sess.CreatedAt.Equal(now))

	loaded, err := client.LoadSession(context.Background(), "sess-1")
	require.NoError(t, err)
	require.Equal(t, sess, loaded)

	end := now.Add(time.Minute)
	ended, err := client.EndSession(context.Background(), "sess-1", end)
	require.NoError(t, err)
	require.Equal(t, session.StatusEnded, ended.Status)
	require.NotNil(t, ended.EndedAt)
	require.True(t, ended.EndedAt.UTC().Equal(end))
}

func TestCreateSessionIsIdempotent(t *testing.T) {
	client := mustNewTestClient()
	now := time.Now().UTC()
	sess, err := client.CreateSession(context.Background(), "sess-1", now)
	require.NoError(t, err)
	require.Equal(t, "sess-1", sess.ID)
	require.Equal(t, session.StatusActive, sess.Status)
	require.True(t, sess.CreatedAt.Equal(now))

	later := now.Add(10 * time.Second)
	again, err := client.CreateSession(context.Background(), "sess-1", later)
	require.NoError(t, err)
	require.Equal(t, "sess-1", again.ID)
	require.Equal(t, session.StatusActive, again.Status)
	require.True(t, again.CreatedAt.Equal(now))
}

func TestUpsertAndLoad(t *testing.T) {
	client := mustNewTestClientWithSessions(t, testSessionID)
	run := session.RunMeta{
		RunID:       "run-1",
		AgentID:     testAgentChat,
		SessionID:   testSessionID,
		Status:      session.RunStatusPending,
		Labels:      map[string]string{"org": "demo"},
		PromptRefs:  []prompt.PromptRef{{ID: prompt.Ident("prompt.a"), Version: "v1"}},
		ChildRunIDs: []string{testRunTwo, testRunThree},
		Metadata:    map[string]any{"reason": "test"},
	}
	err := client.UpsertRun(context.Background(), run)
	require.NoError(t, err)

	stored, err := client.LoadRun(context.Background(), "run-1")
	require.NoError(t, err)
	require.Equal(t, run.RunID, stored.RunID)
	require.Equal(t, run.AgentID, stored.AgentID)
	require.Equal(t, run.SessionID, stored.SessionID)
	require.Equal(t, run.Status, stored.Status)
	require.Equal(t, "demo", stored.Labels["org"])
	require.Equal(t, []prompt.PromptRef{{ID: prompt.Ident("prompt.a"), Version: "v1"}}, stored.PromptRefs)
	// child_run_ids is exclusively managed by LinkChildRun; UpsertRun must
	// ignore caller-provided ChildRunIDs.
	require.Empty(t, stored.ChildRunIDs)

	run.Status = session.RunStatusCompleted
	err = client.UpsertRun(context.Background(), run)
	require.NoError(t, err)
	updated, err := client.LoadRun(context.Background(), "run-1")
	require.NoError(t, err)
	require.Equal(t, session.RunStatusCompleted, updated.Status)
	require.True(t, updated.UpdatedAt.After(updated.StartedAt) || updated.UpdatedAt.Equal(updated.StartedAt))
}

func TestRunDocumentMetadataBSONRoundTripPreservesGoContainerTypes(t *testing.T) {
	doc := runDocument{
		RunID: "run-1",
		Metadata: map[string]any{
			"error": map[string]any{
				"details": []any{
					map[string]any{"field": "tenant", "value": "acme"},
				},
			},
		},
	}

	encoded, err := bson.Marshal(doc)
	require.NoError(t, err)
	var decoded runDocument
	require.NoError(t, bson.Unmarshal(encoded, &decoded))

	run := decoded.toRunMeta()
	require.Equal(t, doc.Metadata, run.Metadata)
	require.IsType(t, map[string]any{}, run.Metadata["error"])
	details := run.Metadata["error"].(map[string]any)["details"]
	require.IsType(t, []any{}, details)
	require.IsType(t, map[string]any{}, details.([]any)[0])
}

func TestLinkChildRun(t *testing.T) {
	client := mustNewTestClientWithSessions(t, testSessionID)
	now := time.Now().UTC()
	require.NoError(t, client.UpsertRun(context.Background(), session.RunMeta{
		RunID:     "run-parent",
		AgentID:   "agent.parent",
		SessionID: testSessionID,
		Status:    session.RunStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}))
	require.NoError(t, client.LinkChildRun(context.Background(), "run-parent", session.RunMeta{
		RunID:     testChildRun,
		AgentID:   testChildAgent,
		SessionID: testSessionID,
		Status:    session.RunStatusPending,
	}))
	require.NoError(t, client.LinkChildRun(context.Background(), "run-parent", session.RunMeta{
		RunID:     testChildRun,
		AgentID:   testChildAgent,
		SessionID: testSessionID,
		Status:    session.RunStatusPending,
	}))

	parent, err := client.LoadRun(context.Background(), "run-parent")
	require.NoError(t, err)
	require.Equal(t, []string{testChildRun}, parent.ChildRunIDs)

	child, err := client.LoadRun(context.Background(), testChildRun)
	require.NoError(t, err)
	require.Equal(t, testChildAgent, child.AgentID)
	require.Equal(t, session.RunStatusPending, child.Status)
}

func TestUpsertRunDoesNotClobberLinkedChildren(t *testing.T) {
	client := mustNewTestClientWithSessions(t, testSessionID)
	now := time.Now().UTC()
	require.NoError(t, client.UpsertRun(context.Background(), session.RunMeta{
		RunID:     "run-parent",
		AgentID:   "agent.parent",
		SessionID: testSessionID,
		Status:    session.RunStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}))

	// Simulate a hook handler's load-modify-write: it loads the parent before
	// any child is linked...
	stale, err := client.LoadRun(context.Background(), "run-parent")
	require.NoError(t, err)
	require.Empty(t, stale.ChildRunIDs)

	// ...then LinkChildRun commits a child concurrently...
	require.NoError(t, client.LinkChildRun(context.Background(), "run-parent", session.RunMeta{
		RunID:     testChildRun,
		AgentID:   testChildAgent,
		SessionID: testSessionID,
		Status:    session.RunStatusPending,
	}))

	// ...and the hook writes back its stale snapshot. The link must survive.
	stale.Status = session.RunStatusCompleted
	require.NoError(t, client.UpsertRun(context.Background(), stale))

	parent, err := client.LoadRun(context.Background(), "run-parent")
	require.NoError(t, err)
	require.Equal(t, session.RunStatusCompleted, parent.Status)
	require.Equal(t, []string{testChildRun}, parent.ChildRunIDs)
}

func TestLinkChildRunIsIdempotent(t *testing.T) {
	client := mustNewTestClientWithSessions(t, testSessionID)
	now := time.Now().UTC()
	require.NoError(t, client.UpsertRun(context.Background(), session.RunMeta{
		RunID:     "run-parent",
		AgentID:   "agent.parent",
		SessionID: testSessionID,
		Status:    session.RunStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}))

	child := session.RunMeta{
		RunID:     testChildRun,
		AgentID:   testChildAgent,
		SessionID: testSessionID,
		Status:    session.RunStatusPending,
	}
	for range 3 {
		require.NoError(t, client.LinkChildRun(context.Background(), "run-parent", child))
	}

	parent, err := client.LoadRun(context.Background(), "run-parent")
	require.NoError(t, err)
	require.Equal(t, []string{testChildRun}, parent.ChildRunIDs)
}

func TestLinkChildRunValidationError(t *testing.T) {
	client := mustNewTestClient()
	err := client.LinkChildRun(context.Background(), "", session.RunMeta{
		RunID:     testChildRun,
		AgentID:   testChildAgent,
		SessionID: testSessionID,
		Status:    session.RunStatusPending,
	})
	require.ErrorIs(t, err, session.ErrParentRunIDRequired)
}

func TestLinkChildRunSessionMismatchError(t *testing.T) {
	client := mustNewTestClientWithSessions(t, testSessionID)
	now := time.Now().UTC()
	require.NoError(t, client.UpsertRun(context.Background(), session.RunMeta{
		RunID:     "run-parent",
		AgentID:   "agent.parent",
		SessionID: testSessionID,
		Status:    session.RunStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}))

	err := client.LinkChildRun(context.Background(), "run-parent", session.RunMeta{
		RunID:     testChildRun,
		AgentID:   testChildAgent,
		SessionID: "sess-2",
		Status:    session.RunStatusPending,
	})
	require.ErrorIs(t, err, session.ErrRunSessionMismatch)
}

func TestListRunsBySession(t *testing.T) {
	client := mustNewTestClientWithSessions(t, testSessionID, "sess-2")
	now := time.Now().UTC()
	require.NoError(t, client.UpsertRun(context.Background(), session.RunMeta{
		RunID:     "run-1",
		AgentID:   testAgentChat,
		SessionID: testSessionID,
		Status:    session.RunStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}))
	require.NoError(t, client.UpsertRun(context.Background(), session.RunMeta{
		RunID:     testRunTwo,
		AgentID:   testAgentChat,
		SessionID: testSessionID,
		Status:    session.RunStatusPending,
		StartedAt: now,
		UpdatedAt: now,
	}))
	require.NoError(t, client.UpsertRun(context.Background(), session.RunMeta{
		RunID:     testRunThree,
		AgentID:   testAgentChat,
		SessionID: "sess-2",
		Status:    session.RunStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
	}))

	out, err := client.ListRunsBySession(context.Background(), testSessionID, []session.RunStatus{session.RunStatusRunning})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, "run-1", out[0].RunID)
}

func TestUpsertValidation(t *testing.T) {
	client := mustNewTestClient()
	err := client.UpsertRun(context.Background(), session.RunMeta{AgentID: "agent"})
	require.EqualError(t, err, "run id is required")
	err = client.UpsertRun(context.Background(), session.RunMeta{RunID: "run"})
	require.EqualError(t, err, "agent id is required")
	err = client.UpsertRun(context.Background(), session.RunMeta{RunID: "run", AgentID: "agent"})
	require.EqualError(t, err, "session id is required")
}

func TestUpsertRunRequiresActiveSessionForNewRuns(t *testing.T) {
	t.Parallel()

	client := mustNewTestClient()
	run := session.RunMeta{RunID: "run-1", AgentID: testAgentChat, SessionID: testSessionID, Status: session.RunStatusPending}
	require.ErrorIs(t, client.UpsertRun(context.Background(), run), session.ErrSessionNotFound)

	createdAt := time.Now().UTC()
	_, err := client.CreateSession(context.Background(), testSessionID, createdAt)
	require.NoError(t, err)
	require.NoError(t, client.UpsertRun(context.Background(), run))
	_, err = client.EndSession(context.Background(), testSessionID, createdAt.Add(time.Minute))
	require.NoError(t, err)

	run.Status = session.RunStatusCompleted
	require.NoError(t, client.UpsertRun(context.Background(), run))
	newRun := run
	newRun.RunID = "run-2"
	require.ErrorIs(t, client.UpsertRun(context.Background(), newRun), session.ErrSessionEnded)
}

func TestUpsertRunRejectsSessionReassignment(t *testing.T) {
	t.Parallel()

	client := mustNewTestClientWithSessions(t, testSessionID, "sess-2")
	run := session.RunMeta{RunID: "run-1", AgentID: testAgentChat, SessionID: testSessionID, Status: session.RunStatusPending}
	require.NoError(t, client.UpsertRun(context.Background(), run))
	run.SessionID = "sess-2"
	require.ErrorIs(t, client.UpsertRun(context.Background(), run), session.ErrRunSessionImmutable)
}

func TestLoadMissingReturnsNotFound(t *testing.T) {
	client := mustNewTestClient()
	_, err := client.LoadRun(context.Background(), "missing")
	require.ErrorIs(t, err, session.ErrRunNotFound)
}

func TestLoadRequiresID(t *testing.T) {
	client := mustNewTestClient()
	_, err := client.LoadRun(context.Background(), "")
	require.EqualError(t, err, "run id is required")
}

func mustNewTestClient() *client {
	sessions := newFakeSessionsCollection()
	runs := newFakeRunsCollection()
	cl, err := newClientWithCollections(nil, sessions, runs, time.Second)
	if err != nil {
		panic(err)
	}
	return cl
}

func mustNewTestClientWithSessions(t *testing.T, sessionIDs ...string) *client {
	t.Helper()
	client := mustNewTestClient()
	for _, sessionID := range sessionIDs {
		_, err := client.CreateSession(context.Background(), sessionID, time.Now().UTC())
		require.NoError(t, err)
	}
	return client
}

type fakeRunsCollection struct {
	mu           sync.Mutex
	indexCreated int
	docs         map[string]runDocument
}

func newFakeRunsCollection() *fakeRunsCollection {
	return &fakeRunsCollection{docs: make(map[string]runDocument)}
}

func (c *fakeRunsCollection) FindOne(ctx context.Context, filter any, opts ...options.Lister[options.FindOneOptions]) singleResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	runID := filter.(bson.M)["run_id"].(string)
	doc, ok := c.docs[runID]
	if !ok {
		return fakeSingleResult{err: mongodriver.ErrNoDocuments}
	}
	copyDoc := doc
	return fakeSingleResult{doc: &copyDoc}
}

func (c *fakeRunsCollection) Find(ctx context.Context, filter any, opts ...options.Lister[options.FindOptions]) (cursor, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	f := filter.(bson.M)
	sessionID, _ := f["session_id"].(string)
	var allowed map[session.RunStatus]struct{}
	if raw, ok := f["status"].(bson.M); ok {
		if in, ok := raw["$in"].([]session.RunStatus); ok {
			allowed = make(map[session.RunStatus]struct{}, len(in))
			for _, st := range in {
				allowed[st] = struct{}{}
			}
		}
	}
	docs := make([]any, 0, len(c.docs))
	for _, doc := range c.docs {
		if doc.SessionID != sessionID {
			continue
		}
		if allowed != nil {
			if _, ok := allowed[doc.Status]; !ok {
				continue
			}
		}
		copyDoc := doc
		docs = append(docs, &copyDoc)
	}
	return newFakeCursor(docs), nil
}

func (c *fakeRunsCollection) UpdateOne(ctx context.Context, filter any, update any,
	opts ...options.Lister[options.UpdateOneOptions]) (*mongodriver.UpdateResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	runID := filter.(bson.M)["run_id"].(string)
	updateOpts, err := applyTestOptions[options.UpdateOneOptions](opts...)
	if err != nil {
		return nil, err
	}
	upsert := updateOpts.Upsert != nil && *updateOpts.Upsert
	doc, ok := c.docs[runID]
	if !ok {
		if !upsert {
			return &mongodriver.UpdateResult{MatchedCount: 0}, nil
		}
		doc = runDocument{}
	}
	up := update.(bson.M)
	switch set := up["$set"].(type) {
	case nil:
	case runDocument:
		doc = set
	case bson.M:
		if v, ok := set["run_id"].(string); ok {
			doc.RunID = v
		}
		if v, ok := set["agent_id"].(string); ok {
			doc.AgentID = v
		}
		if v, ok := set["session_id"].(string); ok {
			doc.SessionID = v
		}
		if v, ok := set["status"].(session.RunStatus); ok {
			doc.Status = v
		}
		if v, ok := set["updated_at"].(time.Time); ok {
			doc.UpdatedAt = v
		}
		if v, ok := set["labels"].(map[string]string); ok {
			doc.Labels = v
		}
		if v, ok := set["metadata"].(map[string]any); ok {
			doc.Metadata = v
		}
		if v, ok := set["prompt_refs"].([]prompt.PromptRef); ok {
			doc.PromptRefs = v
		}
		if v, ok := set["child_run_ids"].([]string); ok {
			doc.ChildRunIDs = v
		}
	default:
		return nil, errors.New("unsupported $set payload")
	}
	if add, ok := up["$addToSet"].(bson.M); ok {
		v, ok := add["child_run_ids"].(string)
		if !ok {
			return nil, errors.New("unsupported $addToSet payload")
		}
		doc.ChildRunIDs = addUniqueChildRunID(doc.ChildRunIDs, v)
	}
	if soi, ok := up["$setOnInsert"].(bson.M); ok && doc.StartedAt.IsZero() {
		if ts, ok := soi["started_at"].(time.Time); ok {
			doc.StartedAt = ts
		}
	}
	c.docs[runID] = doc
	return &mongodriver.UpdateResult{MatchedCount: 1}, nil
}

// addUniqueChildRunID mirrors Mongo $addToSet semantics for the fake runs
// collection: the value is appended only when not already present.
func addUniqueChildRunID(runIDs []string, runID string) []string {
	for _, current := range runIDs {
		if current == runID {
			return runIDs
		}
	}
	return append(runIDs, runID)
}

func (c *fakeRunsCollection) Indexes() indexView {
	return fakeIndexView{parent: &c.indexCreated}
}

type fakeIndexView struct {
	parent *int
}

func (v fakeIndexView) CreateOne(ctx context.Context, model mongodriver.IndexModel,
	opts ...options.Lister[options.CreateIndexesOptions]) (string, error) {
	if len(model.Keys.(bson.D)) == 0 {
		return "", errors.New("missing keys")
	}
	*v.parent++
	return "run_id_idx", nil
}

type fakeSingleResult struct {
	doc any
	err error
}

func (r fakeSingleResult) Decode(val any) error {
	if r.err != nil {
		return r.err
	}
	switch typed := val.(type) {
	case *runDocument:
		*typed = *(r.doc.(*runDocument))
	case *sessionDocument:
		*typed = *(r.doc.(*sessionDocument))
	default:
		return errors.New("unsupported target")
	}
	return nil
}

type fakeSessionsCollection struct {
	mu           sync.Mutex
	indexCreated int
	docs         map[string]sessionDocument
}

func newFakeSessionsCollection() *fakeSessionsCollection {
	return &fakeSessionsCollection{docs: make(map[string]sessionDocument)}
}

func (c *fakeSessionsCollection) FindOne(ctx context.Context, filter any, opts ...options.Lister[options.FindOneOptions]) singleResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	sessionID := filter.(bson.M)["session_id"].(string)
	doc, ok := c.docs[sessionID]
	if !ok {
		return fakeSingleResult{err: mongodriver.ErrNoDocuments}
	}
	copyDoc := doc
	return fakeSingleResult{doc: &copyDoc}
}

func (c *fakeSessionsCollection) Find(ctx context.Context, filter any, opts ...options.Lister[options.FindOptions]) (cursor, error) {
	return newFakeCursor(nil), nil
}

func (c *fakeSessionsCollection) UpdateOne(ctx context.Context, filter any, update any,
	opts ...options.Lister[options.UpdateOneOptions]) (*mongodriver.UpdateResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	sessionID := filter.(bson.M)["session_id"].(string)
	doc, ok := c.docs[sessionID]
	if !ok {
		doc = sessionDocument{}
	}

	up := update.(bson.M)
	updateOpts, err := applyTestOptions[options.UpdateOneOptions](opts...)
	if err != nil {
		return nil, err
	}
	upsert := false
	if updateOpts.Upsert != nil {
		upsert = *updateOpts.Upsert
	}

	if !ok && upsert {
		if soi, ok := up["$setOnInsert"].(bson.M); ok {
			if v, ok := soi["session_id"].(string); ok {
				doc.SessionID = v
			}
			if v, ok := soi["status"].(session.SessionStatus); ok {
				doc.Status = v
			}
			if v, ok := soi["created_at"].(time.Time); ok {
				doc.CreatedAt = v
			}
			if v, ok := soi["updated_at"].(time.Time); ok {
				doc.UpdatedAt = v
			}
		}
	}

	if setAny, ok := up["$set"]; ok {
		if soi, ok := up["$setOnInsert"].(bson.M); ok {
			if _, ok := soi["created_at"]; ok {
				if set, ok := setAny.(bson.M); ok {
					if _, ok := set["created_at"]; ok {
						return nil, errors.New("conflicting update: created_at is set in both $set and $setOnInsert")
					}
				}
			}
		}
		switch set := setAny.(type) {
		case sessionDocument:
			doc = set
		case bson.M:
			if v, ok := set["session_id"].(string); ok {
				doc.SessionID = v
			}
			if v, ok := set["status"].(session.SessionStatus); ok {
				doc.Status = v
			}
			if v, ok := set["ended_at"].(time.Time); ok {
				doc.EndedAt = &v
			}
			if v, ok := set["updated_at"].(time.Time); ok {
				doc.UpdatedAt = v
			}
		default:
			return nil, errors.New("unsupported $set payload")
		}
	}

	c.docs[sessionID] = doc
	return &mongodriver.UpdateResult{MatchedCount: 1}, nil
}

func (c *fakeSessionsCollection) Indexes() indexView {
	return fakeIndexView{parent: &c.indexCreated}
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
	docs []any
	idx  int
}

func newFakeCursor(docs []any) *fakeCursor {
	return &fakeCursor{docs: docs, idx: -1}
}

func (c *fakeCursor) Close(ctx context.Context) error { return nil }

func (c *fakeCursor) Decode(val any) error {
	if c.idx < 0 || c.idx >= len(c.docs) {
		return errors.New("no document")
	}
	switch typed := val.(type) {
	case *runDocument:
		*typed = *(c.docs[c.idx].(*runDocument))
	case *sessionDocument:
		*typed = *(c.docs[c.idx].(*sessionDocument))
	default:
		return errors.New("unsupported target")
	}
	return nil
}

func (c *fakeCursor) Err() error { return nil }

func (c *fakeCursor) Next(ctx context.Context) bool {
	next := c.idx + 1
	if next >= len(c.docs) {
		return false
	}
	c.idx = next
	return true
}
