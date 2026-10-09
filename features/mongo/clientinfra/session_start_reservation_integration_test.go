package clientinfra_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/stretchr/testify/require"
)

func TestMongoStartReservationOwnershipAndReplay(t *testing.T) {
	client := newMonitoredSessionClient(t, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	_, err := client.CreateSession(ctx, integrationSessionID, time.Now().UTC())
	require.NoError(t, err)
	parent := session.RunMeta{RunID: "parent", AgentID: "agent", SessionID: integrationSessionID, Status: session.RunStatusRunning}
	require.NoError(t, client.UpsertRun(ctx, parent))
	child := session.RunMeta{
		RunID: "child", AgentID: "child-agent", SessionID: integrationSessionID,
		Status: session.RunStatusPending, Metadata: map[string]any{"origin": "first"},
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- client.ReserveRun(ctx, child, "attempt-child", "parent")
		}()
	}
	wg.Wait()
	close(results)
	for result := range results {
		require.NoError(t, result)
	}

	replay := child
	replay.Metadata = map[string]any{"origin": "replay"}
	require.NoError(t, client.ReserveRun(ctx, replay, "attempt-child", "parent"))
	require.ErrorIs(t, client.ReserveRun(ctx, child, "other-attempt", "parent"), session.ErrRunAlreadyExists)
	storedChild, err := client.LoadRun(ctx, child.RunID)
	require.NoError(t, err)
	require.Equal(t, "first", storedChild.Metadata["origin"])

	_, err = client.EndSession(ctx, integrationSessionID, time.Now().UTC())
	require.NoError(t, err)
	// Runtime hook replay after end must accept the already committed link.
	require.NoError(t, client.LinkChildRun(ctx, "parent", child))
	require.ErrorIs(t, client.LinkChildRun(ctx, "parent", session.RunMeta{
		RunID: "new-child", AgentID: "child-agent", SessionID: integrationSessionID, Status: session.RunStatusPending,
	}), session.ErrSessionEnded)
	parentAfter, err := client.LoadRun(ctx, parent.RunID)
	require.NoError(t, err)
	require.Equal(t, []string{"child"}, parentAfter.ChildRunIDs)

	require.NoError(t, client.RejectRun(ctx, child.RunID, "other-attempt"))
	storedChild, err = client.LoadRun(ctx, child.RunID)
	require.NoError(t, err)
	require.Equal(t, session.RunStatusPending, storedChild.Status)
	require.NoError(t, client.RejectRun(ctx, child.RunID, "attempt-child"))
	storedChild, err = client.LoadRun(ctx, child.RunID)
	require.NoError(t, err)
	require.Equal(t, session.RunStatusFailed, storedChild.Status)
}
