package selvedge

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	sv "github.com/CaliLuke/selvedge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeartbeatRecorder(t *testing.T) {
	// Allow real database renewals to run under race-enabled CI contention.
	const heartbeatTimeout = 2 * time.Second
	e := newTestEngine(t)
	var calls atomic.Int32
	healthy := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)
	require.NoError(t, e.RegisterExecuteToolActivity(t.Context(), "heartbeat", engine.ActivityOptions{StartToCloseTimeout: 20 * time.Second, HeartbeatTimeout: heartbeatTimeout, RetryPolicy: engine.RetryPolicy{MaxAttempts: 1}}, func(ctx context.Context, _ *api.ToolInput) (*api.ToolOutput, error) {
		defer func() {
			stopped <- struct{}{}
		}()
		calls.Add(1)
		if engine.ActivityHeartbeatTimeout(ctx) != heartbeatTimeout || !engine.RecordActivityHeartbeat(ctx, "not persisted") {
			return nil, errors.New("missing heartbeat metadata or recorder")
		}
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		active := time.NewTimer(3 * heartbeatTimeout)
		defer active.Stop()
		for {
			select {
			case <-ticker.C:
				engine.RecordActivityHeartbeat(ctx, "progress")
			case <-active.C:
				healthy <- struct{}{}
				<-ctx.Done()
				return nil, ctx.Err()
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}))
	require.NoError(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "heartbeat-run", Handler: toolWorkflow("heartbeat")}))
	require.NoError(t, e.SealRegistration(t.Context()))
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	h, err := e.StartWorkflow(ctx, engine.WorkflowStartRequest{ID: "heartbeat-run", Workflow: "heartbeat-run", Input: &api.RunInput{RunID: "heartbeat-run"}})
	require.NoError(t, err)
	_, err = h.Wait(ctx)
	var timeout *sv.ActivityTimeoutError
	require.ErrorAs(t, err, &timeout)
	assert.Equal(t, sv.TimeoutHeartbeat, timeout.Kind)
	var translated *engine.TimeoutError
	require.ErrorAs(t, err, &translated)
	assert.EqualValues(t, 1, calls.Load())
	waitChannel(t, healthy)
	waitChannel(t, stopped)
}

func TestTimeoutMapping(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  engine.ActivityOptions
		want  sv.TimeoutKind
		calls int32
	}{
		{"schedule", engine.ActivityOptions{Queue: "unserved", ScheduleToStartTimeout: 80 * time.Millisecond, RetryPolicy: engine.RetryPolicy{MaxAttempts: 3}}, sv.TimeoutScheduleToStart, 0},
		{"start", engine.ActivityOptions{StartToCloseTimeout: 80 * time.Millisecond, RetryPolicy: engine.RetryPolicy{MaxAttempts: 2, InitialInterval: time.Millisecond}}, sv.TimeoutStartToClose, 2},
		{"heartbeat", engine.ActivityOptions{StartToCloseTimeout: 3 * time.Second, HeartbeatTimeout: 80 * time.Millisecond, RetryPolicy: engine.RetryPolicy{MaxAttempts: 2, InitialInterval: time.Millisecond}}, sv.TimeoutHeartbeat, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := NewWorker(Options{App: testApp(t, filepath.Join(t.TempDir(), "s.db"), sv.WithActivityQueues(""))})
			require.NoError(t, err)
			t.Cleanup(func() {
				assert.NoError(t, e.Close())
			})
			var calls atomic.Int32
			require.NoError(t, e.RegisterExecuteToolActivity(t.Context(), "timeout", tc.opts, func(ctx context.Context, _ *api.ToolInput) (*api.ToolOutput, error) {
				calls.Add(1)
				<-ctx.Done()
				return nil, ctx.Err()
			}))
			err = runWorkflow(t, e, "timeout-run", toolWorkflow("timeout"))
			var timeout *sv.ActivityTimeoutError
			require.ErrorAs(t, err, &timeout)
			assert.Equal(t, tc.want, timeout.Kind)
			var translated *engine.TimeoutError
			require.ErrorAs(t, err, &translated)
			assert.Equal(t, tc.calls, calls.Load())
			tasks, listErr := sv.ListActivityTasks(t.Context(), e.client, "timeout-run")
			require.NoError(t, listErr)
			require.Len(t, tasks, 1)
			assert.EqualValues(t, tc.calls, tasks[0].Attempts)
			assert.Equal(t, "completed", tasks[0].Status)
		})
	}
}

func TestActivityAttemptsUniqueAcrossRestart(t *testing.T) {
	db := filepath.Join(t.TempDir(), "s.db")
	client := testApp(t, db)
	require.NoError(t, client.Start(t.Context()))
	t.Cleanup(func() {
		assert.NoError(t, client.Shutdown(context.Background()))
	})
	attempts := make(chan int, 4)
	register := func(e *Engine) {
		require.NoError(t, e.RegisterExecuteToolActivity(t.Context(), "retry", engine.ActivityOptions{StartToCloseTimeout: 4 * time.Second, RetryPolicy: engine.RetryPolicy{MaxAttempts: 2, InitialInterval: time.Millisecond}}, func(ctx context.Context, _ *api.ToolInput) (*api.ToolOutput, error) {
			n := engine.ActivityAttemptFromContext(ctx)
			attempts <- n
			if n == 1 {
				return nil, errors.New("retryable failure")
			}
			if n == 2 {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return &api.ToolOutput{}, nil
		}))
		require.NoError(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "restart", Handler: toolWorkflow("retry")}))
	}
	worker := func() *Engine {
		e, err := NewWorker(Options{App: testApp(t, db), Client: client})
		require.NoError(t, err)
		t.Cleanup(func() {
			assert.NoError(t, e.Close())
		})
		register(e)
		require.NoError(t, e.SealRegistration(t.Context()))
		return e
	}
	first := worker()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	h, err := first.StartWorkflow(ctx, engine.WorkflowStartRequest{ID: "restart", Workflow: "restart", Input: &api.RunInput{}})
	require.NoError(t, err)
	assert.Equal(t, 1, waitChannel(t, attempts))
	require.Equal(t, 2, waitChannel(t, attempts))
	require.NoError(t, first.Close())
	worker()
	_, err = h.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, waitChannel(t, attempts))
	tasks, err := sv.ListActivityTasks(ctx, client, "restart")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, 3, tasks[0].Attempts)
	assert.Equal(t, 1, tasks[0].Failures)
}

func toolWorkflow(name string) engine.WorkflowFunc {
	return func(w engine.WorkflowContext, _ *api.RunInput) (*api.RunOutput, error) {
		_, err := w.ExecuteToolActivity(w.Context(), engine.ToolActivityCall{Name: name, Input: &api.ToolInput{}})
		return &api.RunOutput{}, err
	}
}
