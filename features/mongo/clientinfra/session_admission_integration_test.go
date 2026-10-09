package clientinfra_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/event"
)

func TestMongoAdmissionVsEnd(t *testing.T) {
	for _, mode := range []string{"new run", "new child", "existing child"} {
		t.Run(mode, func(t *testing.T) {
			reached := make(chan struct{})
			release := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			defer unblock()
			var blocked atomic.Bool
			monitor := &event.CommandMonitor{Succeeded: func(ctx context.Context, command *event.CommandSucceededEvent) {
				namespace, ok := command.Reply.Lookup("cursor", "ns").StringValueOK()
				if ctx.Value(sessionBarrierKey{}) != true || command.CommandName != "find" || !ok || !strings.HasSuffix(namespace, ".agent_sessions") || !blocked.CompareAndSwap(false, true) {
					return
				}
				close(reached)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}}
			client := newMonitoredSessionClient(t, monitor)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			created := time.Unix(100, 0).UTC()
			_, err := client.CreateSession(ctx, integrationSessionID, created)
			require.NoError(t, err)
			parent := session.RunMeta{AgentID: "agent", RunID: "parent", SessionID: integrationSessionID, Status: session.RunStatusCompleted}
			require.NoError(t, client.UpsertRun(ctx, parent))
			candidate := session.RunMeta{AgentID: "agent", RunID: "candidate", SessionID: integrationSessionID, Status: session.RunStatusPending}
			if mode == "existing child" {
				candidate.Status = session.RunStatusCompleted
				require.NoError(t, client.UpsertRun(ctx, candidate))
			}

			done := make(chan error, 1)
			go func() {
				admissionCtx := context.WithValue(ctx, sessionBarrierKey{}, true)
				if mode == "new run" {
					done <- client.UpsertRun(admissionCtx, candidate)
					return
				}
				done <- client.LinkChildRun(admissionCtx, parent.RunID, candidate)
			}()
			select {
			case <-reached:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			_, err = client.EndSession(ctx, integrationSessionID, created.Add(time.Hour))
			require.NoError(t, err)
			swept, err := client.ListRunsBySession(ctx, integrationSessionID, []session.RunStatus{session.RunStatusPending, session.RunStatusRunning, session.RunStatusPaused})
			require.NoError(t, err)
			require.Empty(t, swept)
			unblock()
			select {
			case err = <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			require.ErrorIs(t, err, session.ErrSessionEnded)
			storedParent, err := client.LoadRun(ctx, parent.RunID)
			require.NoError(t, err)
			assert.Empty(t, storedParent.ChildRunIDs)
			stored, err := client.LoadRun(ctx, candidate.RunID)
			if mode == "existing child" {
				require.NoError(t, err)
				assert.Equal(t, session.RunStatusCompleted, stored.Status)
			} else {
				require.ErrorIs(t, err, session.ErrRunNotFound)
			}
			parent.Metadata = map[string]any{"after_end": true}
			require.NoError(t, client.UpsertRun(ctx, parent))
			updated, err := client.LoadRun(ctx, parent.RunID)
			require.NoError(t, err)
			assert.Equal(t, true, updated.Metadata["after_end"])
		})
	}
}
