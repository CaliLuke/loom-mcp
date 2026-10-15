package runtime

import (
	"context"
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

type readFailureLog struct {
	runlog.Store
	failAt, reads int
	appendErr     error
}

type readFailureSessions struct{ session.Store }

func TestRunReadsKeepDurableEvidenceWhenRepairFails(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		engineError                      error
		sessionError, appendError, empty bool
	}{
		{name: "engine unavailable", engineError: assert.AnError},
		{name: "engine missing", engineError: engine.ErrWorkflowNotFound},
		{name: "session unavailable", sessionError: true},
		{name: "append unavailable", appendError: true},
		{name: "empty log engine unavailable", engineError: assert.AnError, empty: true},
		{name: "empty log session unavailable", sessionError: true, empty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := runloginmem.New()
			if !tc.empty {
				_, err := store.Append(t.Context(), &runlog.Event{EventKey: "phase", RunID: "run", AgentID: "agent", SessionID: "session", Type: hooks.RunPhaseChanged, Payload: []byte(`{"Phase":"planning"}`), Timestamp: time.Unix(1, 0)})
				require.NoError(t, err)
			}
			log := &readFailureLog{Store: store}
			if tc.appendError {
				log.appendErr = assert.AnError
			}
			rt := New(WithEngine(&statusFailureEngine{stubEngine: &stubEngine{runStatus: engine.RunStatusCompleted}, statusErr: tc.engineError}), WithRunEventStore(log))
			if tc.sessionError {
				rt.SessionStore = readFailureSessions{rt.SessionStore}
			}
			page, err := rt.ListRunEvents(t.Context(), "run", "", 10)
			require.NoError(t, err)
			snapshot, err := rt.GetRunSnapshot(t.Context(), "run")
			if tc.empty {
				assert.Empty(t, page.Events)
				require.ErrorIs(t, err, run.ErrNotFound)
				assert.Nil(t, snapshot)
			} else {
				require.NoError(t, err)
				require.Len(t, page.Events, 1)
				assert.Equal(t, hooks.RunPhaseChanged, page.Events[0].Type)
				assert.Equal(t, run.StatusRunning, snapshot.Status)
				assert.Equal(t, run.PhasePlanning, snapshot.Phase)
			}
		})
	}
}

func TestRunReadsPropagateStorageFailuresAtEveryRepairRead(t *testing.T) {
	for _, operation := range []string{"events", "snapshot"} {
		t.Run(operation, func(t *testing.T) {
			invoke := func(failAt int) (int, error) {
				store := runloginmem.New()
				_, err := store.Append(t.Context(), &runlog.Event{EventKey: "phase", RunID: "run", AgentID: "agent", SessionID: "session", TurnID: "turn", Type: hooks.RunPhaseChanged, Payload: []byte(`{"Phase":"planning"}`), Timestamp: time.Unix(1, 0)})
				require.NoError(t, err)
				log := &readFailureLog{Store: store, failAt: failAt}
				rt := New(WithEngine(&stubEngine{runStatus: engine.RunStatusCompleted}), WithRunEventStore(log))
				_, err = rt.CreateSession(t.Context(), "session")
				require.NoError(t, err)
				require.NoError(t, rt.SessionStore.UpsertRun(t.Context(), session.RunMeta{RunID: "run", AgentID: "agent", SessionID: "session", Status: session.RunStatusRunning}))
				if operation == "events" {
					page, readErr := rt.ListRunEvents(t.Context(), "run", "", 1)
					if readErr == nil {
						require.NotEmpty(t, page.Events)
						assert.Equal(t, hooks.RunPhaseChanged, page.Events[0].Type)
						if failAt == 0 {
							require.Len(t, page.Events, 2)
							assert.Equal(t, hooks.RunCompleted, page.Events[1].Type)
						} else {
							require.Len(t, page.Events, 1)
						}
					}
					return log.reads, readErr
				}
				snapshot, readErr := rt.GetRunSnapshot(t.Context(), "run")
				if readErr == nil {
					require.NotNil(t, snapshot)
					assert.Equal(t, "run", snapshot.RunID)
					if failAt == 0 {
						assert.Equal(t, run.StatusCompleted, snapshot.Status)
					} else {
						assert.Equal(t, run.StatusRunning, snapshot.Status)
					}
				}
				return log.reads, readErr
			}
			reads, err := invoke(0)
			require.NoError(t, err)
			require.Greater(t, reads, 3)
			for failed := 1; failed <= reads; failed++ {
				actual, err := invoke(failed)
				require.GreaterOrEqual(t, actual, failed)
				// Reads needed to return the caller's page must fail explicitly;
				// best-effort completion repair may instead preserve the durable page.
				if err != nil {
					require.ErrorIs(t, err, assert.AnError)
				}
			}
		})
	}
}

func TestRunSnapshotRejectsCorruptEventPayloads(t *testing.T) {
	for _, kind := range []hooks.EventType{hooks.ChildRunLinked, hooks.AwaitClarification, hooks.AwaitConfirmation, hooks.AwaitQuestions, hooks.AwaitTypedInput, hooks.AwaitExternalTools, hooks.RunPhaseChanged, hooks.AssistantMessage, hooks.AssistantTurnCommitted, hooks.ToolCallScheduled, hooks.ToolCallUpdated, hooks.ToolResultReceived, hooks.RunCompleted} {
		t.Run(string(kind), func(t *testing.T) {
			store := runloginmem.New()
			_, err := store.Append(t.Context(), &runlog.Event{EventKey: "corrupt", RunID: "run", Type: kind, Payload: []byte(`{"id":`), Timestamp: time.Unix(1, 0)})
			require.NoError(t, err)
			rt := New(WithRunEventStore(store))
			snapshot, err := rt.GetRunSnapshot(t.Context(), "run")
			require.ErrorContains(t, err, "decode")
			assert.Nil(t, snapshot)
		})
	}
}

func (l *readFailureLog) List(ctx context.Context, id, cursor string, limit int) (runlog.Page, error) {
	l.reads++
	if l.reads == l.failAt {
		return runlog.Page{}, assert.AnError
	}
	return l.Store.List(ctx, id, cursor, limit)
}

func (l *readFailureLog) Append(ctx context.Context, event *runlog.Event) (runlog.AppendResult, error) {
	if l.appendErr != nil {
		return runlog.AppendResult{}, l.appendErr
	}
	return l.Store.Append(ctx, event)
}

func (s readFailureSessions) LoadRun(context.Context, string) (session.RunMeta, error) {
	return session.RunMeta{}, assert.AnError
}
