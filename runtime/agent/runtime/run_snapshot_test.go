package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/runtime/agent/runlog"
	runloginmem "github.com/CaliLuke/loom-mcp/runtime/agent/runlog/inmem"
	"github.com/CaliLuke/loom-mcp/runtime/agent/toolerrors"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRunSnapshotDerivesToolStateAndCompletion(t *testing.T) {
	t.Parallel()

	var (
		runID     = "run-1"
		agentID   = agent.Ident("svc.agent")
		sessionID = "sess-1"
		turnID    = "turn-1"
	)

	mk := func(at time.Time, evt hooks.Event) *runlog.Event {
		in, err := hooks.EncodeToHookInput(evt, turnID)
		require.NoError(t, err)
		return &runlog.Event{
			EventKey:  in.EventKey,
			RunID:     runID,
			AgentID:   agentID,
			SessionID: sessionID,
			TurnID:    turnID,
			Type:      in.Type,
			Payload:   in.Payload,
			Timestamp: at.UTC(),
		}
	}

	t0 := time.Unix(10, 0).UTC()
	t1 := time.Unix(11, 0).UTC()
	t2 := time.Unix(12, 0).UTC()
	t3 := time.Unix(13, 0).UTC()

	events := []*runlog.Event{
		mk(t0, hooks.NewRunPhaseChangedEvent(runID, agentID, sessionID, run.PhasePlanning)),
		mk(t1, hooks.NewToolCallScheduledEvent(runID, agentID, sessionID, tools.Ident("svc.tools.search"), "call-1", []byte(`{"q":"x"}`), "q", "", 0)),
		mk(t2, hooks.NewToolResultReceivedEvent(runID, agentID, sessionID, tools.Ident("svc.tools.search"), "call-1", "", nil, nil, nil, "", nil, 250*time.Millisecond, nil, nil, toolerrors.New("boom"))),
		mk(t3, hooks.NewRunCompletedEvent(runID, agentID, sessionID, "failed", run.PhaseFailed, nil)),
	}

	snap, err := newRunSnapshot(events)
	require.NoError(t, err)

	require.Equal(t, runID, snap.RunID)
	require.Equal(t, sessionID, snap.SessionID)
	require.Equal(t, turnID, snap.TurnID)
	require.Equal(t, run.StatusFailed, snap.Status)
	require.Equal(t, run.PhaseFailed, snap.Phase)
	require.Len(t, snap.ToolCalls, 1)
	require.Equal(t, "call-1", snap.ToolCalls[0].ToolCallID)
	require.Equal(t, "boom", snap.ToolCalls[0].ErrorSummary)
	require.Equal(t, 250*time.Millisecond, snap.ToolCalls[0].Duration)
}

func TestNewRunSnapshotProjectsAwaitPauseAndResume(t *testing.T) {
	t.Parallel()

	var (
		runID     = "run-1"
		agentID   = agent.Ident("svc.agent")
		sessionID = "sess-1"
		turnID    = "turn-1"
	)
	questionsTitle := "Pick options"

	mk := func(at time.Time, evt hooks.Event) *runlog.Event {
		in, err := hooks.EncodeToHookInput(evt, turnID)
		require.NoError(t, err)
		return &runlog.Event{
			EventKey:  in.EventKey,
			RunID:     runID,
			AgentID:   agentID,
			SessionID: sessionID,
			TurnID:    turnID,
			Type:      in.Type,
			Payload:   in.Payload,
			Timestamp: at.UTC(),
		}
	}

	cases := []struct {
		name       string
		events     []hooks.Event
		wantStatus run.Status
		wantAwait  *run.AwaitSnapshot
	}{
		{
			name: "await questions pauses run",
			events: []hooks.Event{
				hooks.NewAwaitQuestionsEvent(runID, agentID, sessionID, "await-1", tools.Ident("svc.tools.ask"), "call-1", nil, &questionsTitle, []hooks.AwaitQuestion{{ID: "q1", Prompt: "Which?"}}),
				hooks.NewRunPausedEvent(runID, agentID, sessionID, "await_queue", "runtime", nil, nil),
			},
			wantStatus: run.StatusPaused,
			wantAwait: &run.AwaitSnapshot{
				Kind:       string(hooks.AwaitQuestions),
				ID:         "await-1",
				ToolName:   tools.Ident("svc.tools.ask"),
				ToolCallID: "call-1",
				Title:      questionsTitle,
			},
		},
		{
			name: "await typed input pauses run",
			events: []hooks.Event{
				hooks.NewAwaitTypedInputEvent(runID, agentID, sessionID, "await-2", "Provide input", []byte(`{"type":"object"}`)),
				hooks.NewRunPausedEvent(runID, agentID, sessionID, "await_queue", "runtime", nil, nil),
			},
			wantStatus: run.StatusPaused,
			wantAwait: &run.AwaitSnapshot{
				Kind:  string(hooks.AwaitTypedInput),
				ID:    "await-2",
				Title: "Provide input",
			},
		},
		{
			name: "pause without await surfaces paused status",
			events: []hooks.Event{
				hooks.NewRunPausedEvent(runID, agentID, sessionID, "human", "operator", nil, nil),
			},
			wantStatus: run.StatusPaused,
			wantAwait:  nil,
		},
		{
			name: "resume clears await and paused status",
			events: []hooks.Event{
				hooks.NewAwaitQuestionsEvent(runID, agentID, sessionID, "await-3", tools.Ident("svc.tools.ask"), "call-2", nil, nil, nil),
				hooks.NewRunPausedEvent(runID, agentID, sessionID, "await_queue", "runtime", nil, nil),
				hooks.NewRunResumedEvent(runID, agentID, sessionID, "await_completed", "runtime", nil, 0),
			},
			wantStatus: run.StatusRunning,
			wantAwait:  nil,
		},
		{
			name: "completion overrides paused status",
			events: []hooks.Event{
				hooks.NewAwaitTypedInputEvent(runID, agentID, sessionID, "await-4", "Provide input", []byte(`{"type":"object"}`)),
				hooks.NewRunPausedEvent(runID, agentID, sessionID, "await_queue", "runtime", nil, nil),
				hooks.NewRunCompletedEvent(runID, agentID, sessionID, "success", run.PhaseCompleted, nil),
			},
			wantStatus: run.StatusCompleted,
			wantAwait:  nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			events := make([]*runlog.Event, 0, len(tc.events))
			for i, evt := range tc.events {
				events = append(events, mk(time.Unix(int64(10+i), 0), evt))
			}

			snap, err := newRunSnapshot(events)
			require.NoError(t, err)
			assert.Equal(t, tc.wantStatus, snap.Status)
			assert.Equal(t, tc.wantAwait, snap.Await)
		})
	}
}

func TestGetRunSnapshotReadsThroughStore(t *testing.T) {
	t.Parallel()

	rl := runloginmem.New()
	_, err := rl.Append(context.Background(), &runlog.Event{
		EventKey:  "evt-1",
		RunID:     "run-1",
		AgentID:   agent.Ident("svc.agent"),
		SessionID: "sess-1",
		TurnID:    "turn-1",
		Type:      hooks.RunPhaseChanged,
		Payload:   []byte(`{"phase":"planning"}`),
		Timestamp: time.Unix(1, 0).UTC(),
	})
	require.NoError(t, err)

	rt := &Runtime{
		RunEventStore: rl,
	}

	_, err = rt.GetRunSnapshot(context.Background(), "run-1")
	require.NoError(t, err)
}

func TestListRunEventsSkipsSnapshotReplayForActiveRun(t *testing.T) {
	t.Parallel()

	inner := runloginmem.New()
	_, err := inner.Append(context.Background(), &runlog.Event{
		EventKey:  "evt-1",
		RunID:     "run-1",
		AgentID:   agent.Ident("svc.agent"),
		SessionID: "sess-1",
		TurnID:    "turn-1",
		Type:      hooks.RunPhaseChanged,
		Payload:   []byte(`{"phase":"planning"}`),
		Timestamp: time.Unix(1, 0).UTC(),
	})
	require.NoError(t, err)

	store := &countingRunEventStore{Store: inner}
	rt := &Runtime{
		Engine:        &stubEngine{runStatus: engine.RunStatusRunning},
		RunEventStore: store,
	}

	page, err := rt.ListRunEvents(context.Background(), "run-1", "", 10)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	require.Equal(t, 1, store.listCalls)
}

type countingRunEventStore struct {
	runlog.Store
	listCalls int
}

func (s *countingRunEventStore) List(ctx context.Context, runID string, cursor string, limit int) (runlog.Page, error) {
	s.listCalls++
	return s.Store.List(ctx, runID, cursor, limit)
}
