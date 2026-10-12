package clientinfra_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/sessiontest"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/event"
)

func TestMongoContinuationCrashAtomicity(t *testing.T) {
	var inject atomic.Bool
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Test-only fault hook: cancel after the insert acknowledgement, before the
	// head update. Mongo must abort both writes in the same transaction.
	monitor := &event.CommandMonitor{Succeeded: func(_ context.Context, e *event.CommandSucceededEvent) {
		if e.CommandName == "insert" && inject.CompareAndSwap(true, false) {
			cancel()
		}
	}}
	client := newMonitoredSessionClient(t, monitor)
	cs, ok := any(client).(session.ContinuationStore)
	require.True(t, ok)
	_, err := client.CreateSession(t.Context(), integrationSessionID, time.Now())
	require.NoError(t, err)
	err = sessiontest.CheckContinuation(t.Context(), cs, integrationSessionID, func(_ context.Context, next session.Continuation) error {
		inject.Store(true)
		_, crashErr := cs.AdvanceContinuation(ctx, integrationSessionID, nil, next)
		insertAcknowledged := !inject.Swap(false)
		require.True(t, insertAcknowledged, "crash must occur after the body insert")
		return crashErr
	})
	require.NoError(t, err)
}

func TestMongoContinuationConformance(t *testing.T) {
	client := newMonitoredSessionClient(t, nil)
	_, err := client.CreateSession(t.Context(), integrationSessionID, time.Now())
	require.NoError(t, err)
	require.NoError(t, sessiontest.CheckContinuation(t.Context(), client, integrationSessionID))
}

func TestMongoContinuationConcurrentRounds(t *testing.T) {
	client := newMonitoredSessionClient(t, nil)
	_, err := client.CreateSession(t.Context(), integrationSessionID, time.Now())
	require.NoError(t, err)
	require.NoError(t, sessiontest.CheckContinuationRounds(t.Context(), client, integrationSessionID))
}
