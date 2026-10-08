package clientinfra_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sessionclient "github.com/CaliLuke/loom-mcp/v2/features/session/mongo/clients/mongo"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/event"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type sessionBarrierKey struct{}

type endSessionResult struct {
	session session.Session
	err     error
}

func TestMongoEndSessionPreservesFirstTimestamp(t *testing.T) {
	reached := make(chan struct{})
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	var blocked atomic.Bool
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, command *event.CommandStartedEvent) {
		collection, ok := command.Command.Lookup("update").StringValueOK()
		if ctx.Value(sessionBarrierKey{}) != true || !ok || collection != "agent_sessions" || !blocked.CompareAndSwap(false, true) {
			return
		}
		close(reached)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}}
	client := newMonitoredSessionClient(t, monitor)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	created := time.Unix(100, 0).UTC()
	_, err := client.CreateSession(ctx, integrationSessionID, created)
	require.NoError(t, err)

	secondResult := make(chan endSessionResult, 1)
	go func() {
		ended, err := client.EndSession(context.WithValue(ctx, sessionBarrierKey{}, true), integrationSessionID, created.Add(2*time.Hour))
		secondResult <- endSessionResult{session: ended, err: err}
	}()
	select {
	case <-reached:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	first, err := client.EndSession(ctx, integrationSessionID, created.Add(time.Hour))
	require.NoError(t, err)
	unblock()
	var second endSessionResult
	select {
	case second = <-secondResult:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, second.err)
	require.Equal(t, first.EndedAt, second.session.EndedAt)
	repeated, err := client.EndSession(ctx, integrationSessionID, created.Add(3*time.Hour))
	require.NoError(t, err)
	require.Equal(t, first.EndedAt, repeated.EndedAt)
}

// newMonitoredSessionClient uses real Mongo storage; the monitor controls only test ordering.
func newMonitoredSessionClient(t *testing.T, monitor *event.CommandMonitor) sessionclient.Client {
	t.Helper()
	_, database, address := newMongoIntegrationClientFor(t, mongoReplicaSet)
	driver, err := mongodriver.Connect(options.Client().
		ApplyURI(fmt.Sprintf("mongodb://%s/?directConnection=true&replicaSet=rs0", address)).
		SetMonitor(monitor))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, driver.Disconnect(context.Background()))
	})
	client, err := sessionclient.New(sessionclient.Options{Client: driver, Database: database, Timeout: 15 * time.Second})
	require.NoError(t, err)
	return client
}
