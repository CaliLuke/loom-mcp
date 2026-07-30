package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	sessioninmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/stream"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/stretchr/testify/require"
)

type failingStreamSink struct {
	err error
}

func (s failingStreamSink) Send(ctx context.Context, event stream.Event) error {
	return s.err
}

func (s failingStreamSink) Close(ctx context.Context) error {
	return nil
}

func TestHookActivity_StreamFailureIsBestEffortWhileSessionActive(t *testing.T) {
	t.Parallel()

	streamErr := errors.New("stream send failed")
	store := sessioninmem.New()
	rl := &recordingRunlog{}

	sub, err := stream.NewSubscriber(failingStreamSink{err: streamErr})
	require.NoError(t, err)

	rt := &Runtime{
		RunEventStore:    rl,
		Bus:              hooks.NewBus(),
		SessionStore:     store,
		streamSubscriber: sub,
		logger:           telemetry.NoopLogger{},
		tracer:           telemetry.NoopTracer{},
	}

	now := time.Now().UTC()
	_, err = store.CreateSession(context.Background(), "sess-1", now)
	require.NoError(t, err)

	input, err := hooks.EncodeToHookInput(hooks.NewPlannerNoteEvent("run-1", "svc.agent", "sess-1", "note", nil), "turn-1")
	require.NoError(t, err)

	err = rt.hookActivity(context.Background(), input)
	require.NoError(t, err)
	require.Len(t, rl.events, 1, "expected canonical run log append even when stream send fails")
}

func TestHookActivity_StreamFailureNoopAfterSessionEnded(t *testing.T) {
	t.Parallel()

	streamErr := errors.New("stream send failed")
	store := sessioninmem.New()
	rl := &recordingRunlog{}

	sub, err := stream.NewSubscriber(failingStreamSink{err: streamErr})
	require.NoError(t, err)

	rt := &Runtime{
		RunEventStore:    rl,
		Bus:              hooks.NewBus(),
		SessionStore:     store,
		streamSubscriber: sub,
		logger:           telemetry.NoopLogger{},
		tracer:           telemetry.NoopTracer{},
	}

	now := time.Now().UTC()
	_, err = store.CreateSession(context.Background(), "sess-1", now)
	require.NoError(t, err)
	_, err = store.EndSession(context.Background(), "sess-1", now.Add(time.Second))
	require.NoError(t, err)

	input, err := hooks.EncodeToHookInput(hooks.NewPlannerNoteEvent("run-1", "svc.agent", "sess-1", "note", nil), "turn-1")
	require.NoError(t, err)

	err = rt.hookActivity(context.Background(), input)
	require.NoError(t, err)

	// runlog append remains canonical even after session end.
	require.Len(t, rl.events, 1)
	require.Equal(t, "run-1", rl.events[0].RunID)
	require.Equal(t, hooks.PlannerNote, rl.events[0].Type)
}

func TestPublishHookStreamEventCachesEndedSession(t *testing.T) {
	t.Parallel()

	store := &countingSessionStore{Store: sessioninmem.New()}
	sub, err := stream.NewSubscriber(failingStreamSink{})
	require.NoError(t, err)

	rt := &Runtime{
		SessionStore:     store,
		streamSubscriber: sub,
		logger:           telemetry.NoopLogger{},
	}

	now := time.Now().UTC()
	_, err = store.CreateSession(context.Background(), "sess-1", now)
	require.NoError(t, err)
	_, err = store.EndSession(context.Background(), "sess-1", now.Add(time.Second))
	require.NoError(t, err)

	evt := hooks.NewPlannerNoteEvent("run-1", "svc.agent", "sess-1", "note", nil)
	rt.publishHookStreamEvent(context.Background(), "sess-1", evt)
	rt.publishHookStreamEvent(context.Background(), "sess-1", evt)

	require.Equal(t, int64(1), store.loadCalls.Load())
}

func TestMarkStreamSessionEndedBoundsCacheAndEvictsOldest(t *testing.T) {
	const cacheSize = 4096
	rt := &Runtime{}

	for i := 0; i <= cacheSize; i++ {
		rt.markStreamSessionEnded(fmt.Sprintf("sess-%04d", i))
	}

	require.Len(t, rt.endedStreamSessions, cacheSize)
	require.False(t, rt.streamSessionEnded("sess-0000"))
	require.True(t, rt.streamSessionEnded("sess-0001"))
	require.True(t, rt.streamSessionEnded(fmt.Sprintf("sess-%04d", cacheSize)))
}

func TestMarkStreamSessionEndedDoesNotDuplicateOrderEntries(t *testing.T) {
	rt := &Runtime{}

	rt.markStreamSessionEnded("sess-1")
	rt.markStreamSessionEnded("sess-1")

	require.Len(t, rt.endedStreamSessions, 1)
	require.Equal(t, []string{"sess-1"}, rt.endedStreamSessionOrder)
}

func TestMarkStreamSessionEndedSupportsConcurrentWriters(t *testing.T) {
	const sessions = 256
	rt := &Runtime{}
	var wg sync.WaitGroup

	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(sessionID string) {
			defer wg.Done()
			rt.markStreamSessionEnded(sessionID)
		}(fmt.Sprintf("sess-%04d", i))
	}
	wg.Wait()

	require.Len(t, rt.endedStreamSessions, sessions)
	require.Len(t, rt.endedStreamSessionOrder, sessions)
}

var _ runlog.Store = (*recordingRunlog)(nil)
var _ session.Store = (*sessioninmem.Store)(nil)

type countingSessionStore struct {
	session.Store
	loadCalls atomic.Int64
}

func (s *countingSessionStore) LoadSession(ctx context.Context, sessionID string) (session.Session, error) {
	s.loadCalls.Add(1)
	return s.Store.LoadSession(ctx, sessionID)
}
