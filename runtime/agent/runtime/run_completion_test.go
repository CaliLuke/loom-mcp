package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/internal/cancellation"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	runloginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
)

type cyclicCompletionError struct {
	next error
}

type hostileWorkflowHandle struct {
	err error
}

type hostileCompletionQuerier struct {
	err error
}

type fireAndForgetEngine struct {
	*stubEngine
	handle engine.WorkflowHandle
}

type blockingWorkflowHandle struct {
	waitStarted chan struct{}
	complete    chan struct{}
	waitCalls   atomic.Int32
}

func (*cyclicCompletionError) Error() string {
	return "cyclic completion error"
}

func (e *cyclicCompletionError) Unwrap() error {
	return e.next
}

func (h *hostileWorkflowHandle) Wait(context.Context) (*api.RunOutput, error) {
	return nil, h.err
}

func (*hostileWorkflowHandle) Signal(context.Context, string, any) error {
	return nil
}

func (*hostileWorkflowHandle) Cancel(context.Context) error {
	return nil
}

func (q *hostileCompletionQuerier) QueryRunCompletion(context.Context, string) (*api.RunOutput, error) {
	return nil, q.err
}

func (e *fireAndForgetEngine) StartWorkflow(context.Context, engine.WorkflowStartRequest) (engine.WorkflowHandle, error) {
	return e.handle, nil
}

func (h *blockingWorkflowHandle) Wait(ctx context.Context) (*api.RunOutput, error) {
	if h.waitCalls.Add(1) == 1 {
		close(h.waitStarted)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.complete:
		return &api.RunOutput{}, nil
	}
}

func (*blockingWorkflowHandle) Signal(context.Context, string, any) error {
	return nil
}

func (*blockingWorkflowHandle) Cancel(context.Context) error {
	return nil
}

func TestTerminalRunStatusForErrorClassifiesCompleteGraph(t *testing.T) {
	t.Parallel()

	cleanup := errors.New("cleanup failed")
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "success", want: runStatusSuccess},
		{name: "cancellation", err: context.Canceled, want: runStatusCanceled},
		{name: "joined cancellations", err: errors.Join(context.Canceled, context.Canceled), want: runStatusCanceled},
		{name: "mixed cancellation and failure", err: errors.Join(context.Canceled, cleanup), want: runStatusFailed},
		{name: "deadline", err: context.DeadlineExceeded, want: runStatusFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, terminalRunStatusForError(test.err))
		})
	}
}

func TestTerminalRunStatusForErrorRejectsCycleWithoutHanging(t *testing.T) {
	t.Parallel()

	cyclic := &cyclicCompletionError{}
	cyclic.next = cyclic
	result := make(chan string, 1)
	go func() {
		result <- terminalRunStatusForError(cyclic)
	}()
	select {
	case status := <-result:
		require.Equal(t, runStatusFailed, status)
	case <-time.After(time.Second):
		require.Fail(t, "terminal error classification hung on a cyclic graph")
	}
}

func TestObservedCompletionSanitizesHostileError(t *testing.T) {
	t.Parallel()

	cyclic := &cyclicCompletionError{}
	cyclic.next = cyclic
	recorder := &recordingHooks{}
	rt := New(WithHooks(recorder), WithRunEventStore(runloginmem.New()))
	handle := newObservedWorkflowHandle(rt, &RunInput{
		AgentID: "svc.agent",
		RunID:   "observed-hostile",
		TurnID:  "turn-hostile",
	}, &hostileWorkflowHandle{err: cyclic})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := handle.Wait(ctx)
	require.ErrorIs(t, err, cancellation.ErrInvalidErrorGraph)
	completed := lastCompletedEvent(recorder.events)
	require.NotNil(t, completed)
	require.Equal(t, runStatusFailed, completed.Status)
	require.EqualError(t, completed.Error, cancellation.ErrInvalidErrorGraph.Error())
}

func TestClientForStartReleasesCompletedFireAndForgetHandle(t *testing.T) {
	t.Parallel()

	const runID = "fire-and-forget"
	inner := &blockingWorkflowHandle{
		waitStarted: make(chan struct{}),
		complete:    make(chan struct{}),
	}
	rt := New(
		WithEngine(&fireAndForgetEngine{stubEngine: &stubEngine{}, handle: inner}),
		WithRunEventStore(runloginmem.New()),
	)
	_, err := rt.CreateSession(context.Background(), "session")
	require.NoError(t, err)
	client := rt.MustClientFor(AgentRoute{
		ID:               "svc.agent",
		WorkflowName:     "svc.workflow",
		DefaultTaskQueue: "svc.queue",
	})

	var (
		handle   engine.WorkflowHandle
		startErr error
	)
	startDone := make(chan struct{})
	go func() {
		handle, startErr = client.Start(context.Background(), "session", nil, WithRunID(runID))
		close(startDone)
	}()
	select {
	case <-startDone:
	case <-time.After(time.Second):
		t.Fatal("fire-and-forget start blocked on workflow completion")
	}
	require.NoError(t, startErr)
	require.NotNil(t, handle)
	select {
	case <-inner.waitStarted:
	case <-time.After(time.Second):
		t.Fatal("fire-and-forget start did not observe workflow completion")
	}
	_, retained := rt.workflowHandle(runID)
	require.True(t, retained)

	close(inner.complete)
	waitCtx, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	_, err = handle.Wait(waitCtx)
	require.NoError(t, err)
	require.Equal(t, int32(1), inner.waitCalls.Load())
	require.Eventually(t, func() bool {
		_, ok := rt.workflowHandle(runID)
		return !ok
	}, time.Second, 10*time.Millisecond)
}

func TestQueriedCompletionSanitizesHostileError(t *testing.T) {
	t.Parallel()

	cyclic := &cyclicCompletionError{}
	cyclic.next = cyclic
	recorder := &recordingHooks{}
	rt := New(WithHooks(recorder), WithRunEventStore(runloginmem.New()))
	ctx := context.Background()
	runID := "queried-hostile"
	require.NoError(t, rt.publishHookErr(ctx, hooks.NewRunStartedEvent(runID, "svc.agent", run.Context{
		RunID:  runID,
		TurnID: "turn-hostile",
	}, nil), "turn-hostile"))

	result := make(chan error, 1)
	go func() {
		result <- rt.repairQueriedTerminalRunCompletion(ctx, runID, &hostileCompletionQuerier{err: cyclic})
	}()
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("queried completion repair hung on a cyclic error")
	}
	completed := lastCompletedEvent(recorder.events)
	require.NotNil(t, completed)
	require.Equal(t, runStatusFailed, completed.Status)
	require.EqualError(t, completed.Error, cancellation.ErrInvalidErrorGraph.Error())
}

func TestTerminalRunStatusForEngineStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  engine.RunStatus
		want    string
		wantErr string
	}{
		{name: "completed", status: engine.RunStatusCompleted, want: runStatusSuccess},
		{name: "timed_out", status: engine.RunStatusTimedOut, want: runStatusFailed},
		{name: "failed", status: engine.RunStatusFailed, want: runStatusFailed},
		{name: "canceled", status: engine.RunStatusCanceled, want: runStatusCanceled},
		{name: "pending_errors", status: engine.RunStatusPending, wantErr: "non-terminal engine run status"},
		{name: "unknown_errors", status: engine.RunStatus("mystery"), wantErr: "unexpected engine run status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := terminalRunStatusForEngineStatus(tt.status)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.Empty(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTerminalRunErrorForStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		status           engine.RunStatus
		wantTerminalErr  error
		wantTerminalText string
		wantMappingErr   string
	}{
		{name: "completed", status: engine.RunStatusCompleted},
		{name: "timed_out", status: engine.RunStatusTimedOut, wantTerminalErr: context.DeadlineExceeded},
		{name: "failed", status: engine.RunStatusFailed, wantTerminalText: "workflow failed before runtime emitted RunCompleted"},
		{name: "canceled", status: engine.RunStatusCanceled, wantTerminalErr: context.Canceled},
		{name: "paused_errors", status: engine.RunStatusPaused, wantMappingErr: "non-terminal engine run status"},
		{name: "unknown_errors", status: engine.RunStatus("mystery"), wantMappingErr: "unexpected engine run status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := terminalRunErrorForStatus(tt.status)
			if tt.wantMappingErr != "" {
				require.ErrorContains(t, err, tt.wantMappingErr)
				require.NoError(t, got)
				return
			}
			require.NoError(t, err)
			if tt.wantTerminalErr != nil {
				require.ErrorIs(t, got, tt.wantTerminalErr)
				return
			}
			if tt.wantTerminalText != "" {
				require.ErrorContains(t, got, tt.wantTerminalText)
				return
			}
			require.NoError(t, got)
		})
	}
}

func lastCompletedEvent(events []hooks.Event) *hooks.RunCompletedEvent {
	for index := len(events) - 1; index >= 0; index-- {
		if completed, ok := events[index].(*hooks.RunCompletedEvent); ok {
			return completed
		}
	}
	return nil
}
