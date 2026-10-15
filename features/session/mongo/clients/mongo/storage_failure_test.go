package mongo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type sessionIOFault struct {
	operations []string
	failAt     int
}

type sessionFaultCollection struct {
	collection
	fault *sessionIOFault
}

func TestSessionOperationsPropagateDatabaseFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(context.Context, *client) error
	}{
		{"create", func(ctx context.Context, c *client) error {
			_, err := c.CreateSession(ctx, "new-session", time.Unix(1, 0))
			return err
		}},
		{"load session", func(ctx context.Context, c *client) error {
			_, err := c.LoadSession(ctx, "session")
			return err
		}},
		{"end", func(ctx context.Context, c *client) error {
			_, err := c.EndSession(ctx, "session", time.Unix(2, 0))
			return err
		}},
		{"load run", func(ctx context.Context, c *client) error {
			_, err := c.LoadRun(ctx, "parent")
			return err
		}},
		{"list", func(ctx context.Context, c *client) error {
			_, err := c.ListRunsBySession(ctx, "session", nil)
			return err
		}},
		{"new run", func(ctx context.Context, c *client) error {
			return c.UpsertRun(ctx, session.RunMeta{RunID: "child", SessionID: "session", AgentID: "agent", Status: session.RunStatusRunning})
		}},
		{"existing run", func(ctx context.Context, c *client) error {
			return c.UpsertRun(ctx, session.RunMeta{RunID: "parent", SessionID: "session", AgentID: "agent", Status: session.RunStatusCompleted})
		}},
		{"reserve", func(ctx context.Context, c *client) error {
			return c.ReserveRun(ctx, session.RunMeta{RunID: "child", SessionID: "session", AgentID: "agent", Status: session.RunStatusPending}, "attempt", "")
		}},
		{"reserve child", func(ctx context.Context, c *client) error {
			return c.ReserveRun(ctx, session.RunMeta{RunID: "child", SessionID: "session", AgentID: "agent", Status: session.RunStatusPending}, "attempt", "parent")
		}},
		{"link child", func(ctx context.Context, c *client) error {
			return c.LinkChildRun(ctx, "parent", session.RunMeta{RunID: "child", SessionID: "session", AgentID: "agent", Status: session.RunStatusRunning})
		}},
		{"reject", func(ctx context.Context, c *client) error {
			return c.RejectRun(ctx, "parent", "attempt")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			baseline, trace := sessionFaultFixture(t, 0)
			require.NoError(t, tc.run(t.Context(), baseline))
			require.NotEmpty(t, trace.operations)
			for i, operation := range trace.operations {
				t.Run(fmt.Sprintf("%02d_%s", i+1, operation), func(t *testing.T) {
					client, fault := sessionFaultFixture(t, i+1)
					require.ErrorIs(t, tc.run(t.Context(), client), assert.AnError)
					assert.GreaterOrEqual(t, len(fault.operations), i+1)
				})
			}
		})
	}
}

func (c *sessionFaultCollection) FindOne(ctx context.Context, filter any, opts ...options.Lister[options.FindOneOptions]) singleResult {
	if c.fault.fails("find one") {
		return fakeSingleResult{err: assert.AnError}
	}
	return c.collection.FindOne(ctx, filter, opts...)
}

func (c *sessionFaultCollection) Find(ctx context.Context, filter any, opts ...options.Lister[options.FindOptions]) (cursor, error) {
	if c.fault.fails("find") {
		return nil, assert.AnError
	}
	return c.collection.Find(ctx, filter, opts...)
}

func (c *sessionFaultCollection) UpdateOne(ctx context.Context, filter, update any, opts ...options.Lister[options.UpdateOneOptions]) (*mongodriver.UpdateResult, error) {
	if c.fault.fails("update") {
		return nil, assert.AnError
	}
	return c.collection.UpdateOne(ctx, filter, update, opts...)
}

func (c *sessionFaultCollection) InsertOne(ctx context.Context, doc any, opts ...options.Lister[options.InsertOneOptions]) (*mongodriver.InsertOneResult, error) {
	if c.fault.fails("insert") {
		return nil, assert.AnError
	}
	return c.collection.InsertOne(ctx, doc, opts...)
}

// sessionFaultFixture seeds real in-memory collection behavior before enabling
// the database boundary fault, so failures exercise valid API operations.
func sessionFaultFixture(t *testing.T, failAt int) (*client, *sessionIOFault) {
	t.Helper()
	c := mustNewTestClientWithSessions(t, "session")
	require.NoError(t, c.UpsertRun(t.Context(), session.RunMeta{RunID: "parent", SessionID: "session", AgentID: "agent", Status: session.RunStatusRunning}))
	fault := &sessionIOFault{failAt: failAt}
	c.sessions = &sessionFaultCollection{collection: c.sessions, fault: fault}
	c.runs = &sessionFaultCollection{collection: c.runs, fault: fault}
	return c, fault
}

func (f *sessionIOFault) fails(operation string) bool {
	f.operations = append(f.operations, operation)
	return len(f.operations) == f.failAt
}
