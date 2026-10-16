package runtime

import (
	"strconv"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	runloginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A process restart can leave the durable log without its final event even
// though the workflow engine has already recorded completion. Reading the tail
// must repair it once and preserve the last original event at page boundaries.
func TestListRunEventsRepairsTerminalTailAfterRestart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status engine.RunStatus
		want   run.Status
	}{
		{"completed", engine.RunStatusCompleted, run.StatusCompleted},
		{"failed", engine.RunStatusFailed, run.StatusFailed},
		{"canceled", engine.RunStatusCanceled, run.StatusCanceled},
		{"timed out", engine.RunStatusTimedOut, run.StatusFailed},
	} {
		for _, limit := range []int{1, 10} {
			t.Run(tc.name+"/"+strconv.Itoa(limit), func(t *testing.T) {
				store := runloginmem.New()
				_, err := store.Append(t.Context(), &runlog.Event{EventKey: "phase", RunID: "run", AgentID: "agent", SessionID: "session", TurnID: "turn", Type: hooks.RunPhaseChanged, Payload: []byte(`{"Phase":"planning"}`), Timestamp: time.Unix(1, 0)})
				require.NoError(t, err)
				runtime := New(WithEngine(&stubEngine{runStatus: tc.status}), WithRunEventStore(store))
				_, err = runtime.SessionStore.CreateSession(t.Context(), "session", time.Unix(1, 0))
				require.NoError(t, err)
				require.NoError(t, runtime.SessionStore.UpsertRun(t.Context(), session.RunMeta{RunID: "run", AgentID: "agent", SessionID: "session", Status: session.RunStatusRunning}))
				page, err := runtime.ListRunEvents(t.Context(), "run", "", limit)
				require.NoError(t, err)
				require.Len(t, page.Events, 2)
				assert.Equal(t, hooks.RunPhaseChanged, page.Events[0].Type)
				assert.Equal(t, hooks.RunCompleted, page.Events[1].Type)
				assert.Equal(t, "agent", string(page.Events[1].AgentID))
				assert.Equal(t, "session", page.Events[1].SessionID)
				assert.Equal(t, "turn", page.Events[1].TurnID)
				snapshot, err := runtime.GetRunSnapshot(t.Context(), "run")
				require.NoError(t, err)
				assert.Equal(t, tc.want, snapshot.Status)
				// Re-reading must not publish a duplicate completion.
				again, err := runtime.ListRunEvents(t.Context(), "run", "", 10)
				require.NoError(t, err)
				require.Len(t, again.Events, 2)
				assert.Equal(t, page.Events[1].ID, again.Events[1].ID)
			})
		}
	}
}
