// Package mongo hosts the MongoDB client used by the session store.
package mongo

//go:generate cmg gen .

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	mongodriver "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"goa.design/clue/health"

	clientinfra "github.com/CaliLuke/loom-mcp/features/mongo/clientinfra"
	"github.com/CaliLuke/loom-mcp/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/runtime/agent/session"
)

const (
	defaultSessionsCollection = "agent_sessions"
	defaultRunsCollection     = "agent_runs"
	defaultOpTimeout          = 5 * time.Second
	sessionClientName         = "session-mongo"
)

// Client exposes Mongo-backed operations for session metadata.
type Client interface {
	health.Pinger

	CreateSession(ctx context.Context, sessionID string, createdAt time.Time) (session.Session, error)
	LoadSession(ctx context.Context, sessionID string) (session.Session, error)
	EndSession(ctx context.Context, sessionID string, endedAt time.Time) (session.Session, error)

	UpsertRun(ctx context.Context, run session.RunMeta) error
	LinkChildRun(ctx context.Context, parentRunID string, child session.RunMeta) error
	LoadRun(ctx context.Context, runID string) (session.RunMeta, error)
	ListRunsBySession(ctx context.Context, sessionID string, statuses []session.RunStatus) ([]session.RunMeta, error)
}

// Options configures the Mongo session client.
type Options struct {
	Client             *mongodriver.Client
	Database           string
	SessionsCollection string
	RunsCollection     string
	Timeout            time.Duration
}

type client struct {
	mongo    *mongodriver.Client
	sessions collection
	runs     collection
	timeout  time.Duration
}

// New returns a Client backed by MongoDB.
func New(opts Options) (Client, error) {
	if err := clientinfra.ValidateMongoOptions(opts.Client, opts.Database); err != nil {
		return nil, err
	}
	sessionsCollection := clientinfra.ResolveCollectionName(opts.SessionsCollection, defaultSessionsCollection)
	runsCollection := clientinfra.ResolveCollectionName(opts.RunsCollection, defaultRunsCollection)
	timeout := clientinfra.ResolveTimeout(opts.Timeout, defaultOpTimeout)
	sessWrapper := clientinfra.NewCollection(opts.Client, opts.Database, sessionsCollection)
	runWrapper := clientinfra.NewCollection(opts.Client, opts.Database, runsCollection)
	if err := clientinfra.EnsureIndexes(timeout, func(ctx context.Context) error {
		return ensureIndexes(ctx, sessWrapper, runWrapper)
	}); err != nil {
		return nil, err
	}
	return newClientWithCollections(opts.Client, sessWrapper, runWrapper, timeout)
}

func (c *client) Name() string {
	return sessionClientName
}

func (c *client) Ping(ctx context.Context) error {
	return clientinfra.Ping(ctx, c.mongo, true)
}

func (c *client) CreateSession(ctx context.Context, sessionID string, createdAt time.Time) (session.Session, error) {
	if err := validateCreateSessionInput(sessionID, createdAt); err != nil {
		return session.Session{}, err
	}
	existing, err := c.loadExistingSession(ctx, sessionID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, session.ErrSessionNotFound) {
		return session.Session{}, err
	}

	now, createdAt := createSessionTimestamps(createdAt)
	ctxWithTimeout, cancel := c.withTimeout(ctx)
	defer cancel()
	filter := bson.M{"session_id": sessionID}
	update := bson.M{
		// Idempotent insert: CreateSession must never modify an existing session.
		//
		// MongoDB rejects updates that set the same path in multiple update
		// operators (e.g. created_at in both $set and $setOnInsert). Keeping this
		// as a pure $setOnInsert update avoids that class of bugs and makes
		// CreateSession safe under retries and races.
		"$setOnInsert": bson.M{
			"session_id": sessionID,
			"status":     session.StatusActive,
			"created_at": createdAt,
			"updated_at": now,
		},
	}
	if _, err := c.sessions.UpdateOne(ctxWithTimeout, filter, update, options.Update().SetUpsert(true)); err != nil {
		return session.Session{}, err
	}

	out, err := c.LoadSession(ctx, sessionID)
	if err != nil {
		return session.Session{}, err
	}
	if out.Status == session.StatusEnded {
		return session.Session{}, session.ErrSessionEnded
	}
	return out, nil
}

func (c *client) LoadSession(ctx context.Context, sessionID string) (session.Session, error) {
	if sessionID == "" {
		return session.Session{}, errors.New("session id is required")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	filter := bson.M{"session_id": sessionID}
	var doc sessionDocument
	if err := c.sessions.FindOne(ctx, filter).Decode(&doc); err != nil {
		if errors.Is(err, mongodriver.ErrNoDocuments) {
			return session.Session{}, session.ErrSessionNotFound
		}
		return session.Session{}, err
	}
	return doc.toSession(), nil
}

func (c *client) EndSession(ctx context.Context, sessionID string, endedAt time.Time) (session.Session, error) {
	if sessionID == "" {
		return session.Session{}, errors.New("session id is required")
	}
	if endedAt.IsZero() {
		return session.Session{}, errors.New("ended_at is required")
	}

	existing, err := c.LoadSession(ctx, sessionID)
	if err != nil {
		return session.Session{}, err
	}
	if existing.Status == session.StatusEnded {
		return existing, nil
	}

	now := time.Now().UTC()
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	filter := bson.M{"session_id": sessionID}
	update := bson.M{
		"$set": bson.M{
			"status":     session.StatusEnded,
			"ended_at":   endedAt.UTC(),
			"updated_at": now,
		},
	}
	if _, err := c.sessions.UpdateOne(ctx, filter, update); err != nil {
		return session.Session{}, err
	}
	return c.LoadSession(ctx, sessionID)
}

func (c *client) UpsertRun(ctx context.Context, run session.RunMeta) error {
	if run.RunID == "" {
		return errors.New("run id is required")
	}
	if run.AgentID == "" {
		return errors.New("agent id is required")
	}
	if run.SessionID == "" {
		return errors.New("session id is required")
	}
	now := time.Now().UTC()
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	run.UpdatedAt = now
	doc := fromRunMeta(run)
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	filter := bson.M{"run_id": run.RunID}
	update := bson.M{
		"$set": bson.M{
			"run_id":        doc.RunID,
			"agent_id":      doc.AgentID,
			"session_id":    doc.SessionID,
			"status":        doc.Status,
			"updated_at":    doc.UpdatedAt,
			"labels":        doc.Labels,
			"prompt_refs":   doc.PromptRefs,
			"child_run_ids": doc.ChildRunIDs,
			"metadata":      doc.Metadata,
		},
		"$setOnInsert": bson.M{
			"started_at": doc.StartedAt,
		},
	}
	_, err := c.runs.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
	return err
}

// LinkChildRun links a child run to a parent run atomically.
func (c *client) LinkChildRun(ctx context.Context, parentRunID string, child session.RunMeta) error {
	if err := session.ValidateChildRunLink(parentRunID, child); err != nil {
		return err
	}
	if c.mongo == nil {
		return c.linkChildRun(ctx, parentRunID, child)
	}
	sessionCtx, err := c.mongo.StartSession()
	if err != nil {
		return err
	}
	defer sessionCtx.EndSession(ctx)
	_, err = sessionCtx.WithTransaction(ctx, func(txCtx mongodriver.SessionContext) (any, error) {
		return nil, c.linkChildRun(txCtx, parentRunID, child)
	})
	return err
}

// linkChildRun applies the child-link mutation set under a single caller-owned
// context (transactional or non-transactional).
//
// Contract:
//   - Parent run must already exist.
//   - Parent and child runs must belong to the same session.
//   - Child run materialization and parent-child linkage are both persisted.
func (c *client) linkChildRun(ctx context.Context, parentRunID string, child session.RunMeta) error {
	parent, err := c.LoadRun(ctx, parentRunID)
	if err != nil {
		return err
	}
	if parent.SessionID != child.SessionID {
		return session.ErrRunSessionMismatch
	}

	existingChild, err := c.LoadRun(ctx, child.RunID)
	switch {
	case err == nil:
		if existingChild.SessionID != parent.SessionID {
			return session.ErrRunSessionMismatch
		}
		if err := c.UpsertRun(ctx, existingChild); err != nil {
			return err
		}
	case errors.Is(err, session.ErrRunNotFound):
		if err := c.UpsertRun(ctx, child); err != nil {
			return err
		}
	default:
		return err
	}

	parent.ChildRunIDs = appendUniqueRunID(parent.ChildRunIDs, child.RunID)
	return c.UpsertRun(ctx, parent)
}

func (c *client) LoadRun(ctx context.Context, runID string) (session.RunMeta, error) {
	if runID == "" {
		return session.RunMeta{}, errors.New("run id is required")
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	filter := bson.M{"run_id": runID}
	var doc runDocument
	if err := c.runs.FindOne(ctx, filter).Decode(&doc); err != nil {
		if errors.Is(err, mongodriver.ErrNoDocuments) {
			return session.RunMeta{}, session.ErrRunNotFound
		}
		return session.RunMeta{}, err
	}
	return doc.toRunMeta(), nil
}

func (c *client) ListRunsBySession(ctx context.Context, sessionID string, statuses []session.RunStatus) ([]session.RunMeta, error) {
	if sessionID == "" {
		return nil, errors.New("session id is required")
	}
	filter := bson.M{"session_id": sessionID}
	if len(statuses) > 0 {
		filter["status"] = bson.M{"$in": statuses}
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	cur, err := c.runs.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "started_at", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = cur.Close(ctx)
	}()
	var out []session.RunMeta
	for cur.Next(ctx) {
		var doc runDocument
		if err := cur.Decode(&doc); err != nil {
			return nil, err
		}
		out = append(out, doc.toRunMeta())
	}
	if err := cur.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return clientinfra.WithTimeout(ctx, c.timeout, true)
}

type runDocument struct {
	RunID       string             `bson:"run_id"`
	AgentID     string             `bson:"agent_id"`
	SessionID   string             `bson:"session_id,omitempty"`
	Status      session.RunStatus  `bson:"status"`
	StartedAt   time.Time          `bson:"started_at"`
	UpdatedAt   time.Time          `bson:"updated_at"`
	Labels      map[string]string  `bson:"labels,omitempty"`
	PromptRefs  []prompt.PromptRef `bson:"prompt_refs,omitempty"`
	ChildRunIDs []string           `bson:"child_run_ids,omitempty"`
	Metadata    map[string]any     `bson:"metadata,omitempty"`
}

type sessionDocument struct {
	SessionID string                `bson:"session_id"`
	Status    session.SessionStatus `bson:"status"`
	CreatedAt time.Time             `bson:"created_at"`
	EndedAt   *time.Time            `bson:"ended_at,omitempty"`
	UpdatedAt time.Time             `bson:"updated_at"`
}

func fromRunMeta(run session.RunMeta) runDocument {
	return runDocument{
		RunID:       run.RunID,
		AgentID:     run.AgentID,
		SessionID:   run.SessionID,
		Status:      run.Status,
		StartedAt:   run.StartedAt.UTC(),
		UpdatedAt:   run.UpdatedAt.UTC(),
		Labels:      cloneLabels(run.Labels),
		PromptRefs:  clonePromptRefs(run.PromptRefs),
		ChildRunIDs: cloneChildRunIDs(run.ChildRunIDs),
		Metadata:    cloneMetadata(run.Metadata),
	}
}

func (doc runDocument) toRunMeta() session.RunMeta {
	return session.RunMeta{
		RunID:       doc.RunID,
		AgentID:     doc.AgentID,
		SessionID:   doc.SessionID,
		Status:      doc.Status,
		StartedAt:   doc.StartedAt,
		UpdatedAt:   doc.UpdatedAt,
		Labels:      cloneLabels(doc.Labels),
		PromptRefs:  clonePromptRefs(doc.PromptRefs),
		ChildRunIDs: cloneChildRunIDs(doc.ChildRunIDs),
		Metadata:    cloneMetadata(doc.Metadata),
	}
}

func (doc sessionDocument) toSession() session.Session {
	var endedAt *time.Time
	if doc.EndedAt != nil {
		at := doc.EndedAt.UTC()
		endedAt = &at
	}
	return session.Session{
		ID:        doc.SessionID,
		Status:    doc.Status,
		CreatedAt: doc.CreatedAt.UTC(),
		EndedAt:   endedAt,
	}
}

func cloneLabels(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneMetadata(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// clonePromptRefs clones prompt refs to prevent callers mutating stored state.
func clonePromptRefs(src []prompt.PromptRef) []prompt.PromptRef {
	if len(src) == 0 {
		return nil
	}
	dst := make([]prompt.PromptRef, len(src))
	copy(dst, src)
	return dst
}

func cloneChildRunIDs(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

func appendUniqueRunID(runIDs []string, runID string) []string {
	for _, current := range runIDs {
		if current == runID {
			return runIDs
		}
	}
	return append(runIDs, runID)
}

func ensureIndexes(ctx context.Context, sessionsColl, runsColl collection) error {
	sessionIndex := mongodriver.IndexModel{
		Keys:    bson.D{{Key: "session_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	}
	if _, err := sessionsColl.Indexes().CreateOne(ctx, sessionIndex); err != nil {
		return err
	}
	runIndex := mongodriver.IndexModel{
		Keys:    bson.D{{Key: "run_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	}
	if _, err := runsColl.Indexes().CreateOne(ctx, runIndex); err != nil {
		return err
	}
	runSessionIndex := mongodriver.IndexModel{
		Keys: bson.D{{Key: "session_id", Value: 1}},
	}
	if _, err := runsColl.Indexes().CreateOne(ctx, runSessionIndex); err != nil {
		return err
	}
	runSessionStatusIndex := mongodriver.IndexModel{
		Keys: bson.D{
			{Key: "session_id", Value: 1},
			{Key: "status", Value: 1},
		},
	}
	if _, err := runsColl.Indexes().CreateOne(ctx, runSessionStatusIndex); err != nil {
		return err
	}
	return nil
}

func newClientWithCollections(mongoClient *mongodriver.Client, sessionsColl, runsColl collection, timeout time.Duration) (*client, error) {
	if err := clientinfra.ValidateCollections("collections are required", sessionsColl, runsColl); err != nil {
		return nil, err
	}
	timeout = clientinfra.ResolveTimeout(timeout, defaultOpTimeout)
	return &client{
		mongo:    mongoClient,
		sessions: sessionsColl,
		runs:     runsColl,
		timeout:  timeout,
	}, nil
}

func validateCreateSessionInput(sessionID string, createdAt time.Time) error {
	if sessionID == "" {
		return errors.New("session id is required")
	}
	if createdAt.IsZero() {
		return errors.New("created_at is required")
	}
	return nil
}

func (c *client) loadExistingSession(ctx context.Context, sessionID string) (session.Session, error) {
	existing, err := c.LoadSession(ctx, sessionID)
	if err != nil {
		return session.Session{}, err
	}
	if existing.Status == session.StatusEnded {
		return session.Session{}, session.ErrSessionEnded
	}
	return existing, nil
}

func createSessionTimestamps(createdAt time.Time) (time.Time, time.Time) {
	return time.Now().UTC(), createdAt.UTC()
}

type collection interface {
	clientinfra.FindOneCollection
	clientinfra.FindCollection
	clientinfra.UpdateOneCollection
	clientinfra.IndexedCollection
}

type singleResult = clientinfra.SingleResultDecoder

type cursor = clientinfra.CursorReader

type indexView = clientinfra.IndexCreator
