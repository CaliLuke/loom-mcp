package runtime

import (
	"context"
	"errors"
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
