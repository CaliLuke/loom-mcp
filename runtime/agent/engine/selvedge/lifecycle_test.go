package selvedge

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	sv "github.com/CaliLuke/selvedge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type migrationBarrier struct {
	fs.FS
	entered, release chan struct{}
	once             sync.Once
	calls            atomic.Int32
	fail             atomic.Bool
	skipFirst        bool
}

func (b *migrationBarrier) Open(name string) (fs.File, error) {
	b.calls.Add(1)
	first := false
	b.once.Do(func() {
		first = true
		close(b.entered)
		<-b.release
	})
	if first && b.skipFirst {
		return nil, fs.ErrNotExist
	}
	if b.fail.Load() && strings.HasSuffix(name, ".sql") {
		return nil, errors.New("injected migration failure")
	}
	return b.FS.Open(name)
}

func testApp(t *testing.T, db string, options ...sv.Option) *sv.App {
	t.Helper()
	opts := make([]sv.Option, 0, 9+len(options))
	opts = append(opts, []sv.Option{sv.WithDatabase(db), sv.WithMaxPollInterval(50 * time.Millisecond), sv.WithActivityPollInterval(10 * time.Millisecond), sv.WithWaitPollInterval(10 * time.Millisecond), sv.WithTimerCheckInterval(10 * time.Millisecond), sv.WithWorkflowResumptionInterval(10 * time.Millisecond), sv.WithLeaderHeartbeatInterval(20 * time.Millisecond), sv.WithLeaderLeaseDuration(100 * time.Millisecond), sv.WithShutdownTimeout(5 * time.Second)}...)
	return sv.NewApp(append(opts, options...)...)
}

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewWorker(Options{App: testApp(t, filepath.Join(t.TempDir(), "s.db"))})
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, e.Close())
	})
	return e
}

func registerEcho(t *testing.T, e *Engine) {
	t.Helper()
	require.NoError(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "echo", Handler: func(_ engine.WorkflowContext, in *api.RunInput) (*api.RunOutput, error) {
		return &api.RunOutput{RunID: in.RunID}, nil
	}}))
}

func waitChannel[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("barrier timed out")
		var zero T
		return zero
	}
}

func TestSealRegistration(t *testing.T) {
	t.Run("successful worker outlives caller", func(t *testing.T) {
		e := newTestEngine(t)
		registerEcho(t, e)
		ctx, cancel := context.WithCancel(t.Context())
		require.NoError(t, e.SealRegistration(ctx))
		cancel()
		require.NoError(t, e.SealRegistration(ctx))
		require.NoError(t, e.SealRegistration(t.Context()))
		require.Error(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "late"}))
		h, err := e.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "after-cancel", Workflow: "echo", Input: &api.RunInput{RunID: "retained"}})
		require.NoError(t, err)
		ctx, cancel = context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		out, err := h.Wait(ctx)
		require.NoError(t, err)
		assert.Equal(t, "retained", out.RunID)
	})
	t.Run("deadline late cleanup and concurrent retry", func(t *testing.T) {
		b := &migrationBarrier{FS: sv.EmbeddedMigrationsFS(), entered: make(chan struct{}), release: make(chan struct{}), skipFirst: true}
		db := filepath.Join(t.TempDir(), "s.db")
		client := testApp(t, db)
		require.NoError(t, client.Start(t.Context()))
		defer func() {
			assert.NoError(t, client.Shutdown(context.Background()))
		}()
		e, err := NewWorker(Options{App: testApp(t, db, sv.WithMigrationsFS(b)), Client: client})
		require.NoError(t, err)
		defer func() {
			releaseBarrier(b.release)
			assert.NoError(t, e.Close())
		}()
		registerEcho(t, e)
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		result := make(chan error, 1)
		go func() {
			result <- e.SealRegistration(ctx)
		}()
		waitChannel(t, b.entered)
		require.ErrorIs(t, waitChannel(t, result), context.DeadlineExceeded)
		require.Error(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "late"}))
		retryCtx, retryCancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer retryCancel()
		require.ErrorIs(t, e.SealRegistration(retryCtx), context.DeadlineExceeded)
		e.mu.Lock()
		pending := e.pending
		e.mu.Unlock()
		close(b.release)
		waitChannel(t, pending.done)
		// The first directory lookup deliberately skips migrations on an
		// already initialized database, allowing Start to succeed late even
		// though its owned lifetime was canceled. Cleanup must close that App.
		_, err = sv.StartWorkflowByName(t.Context(), e.app, "echo", nil, sv.WithInstanceID("late-start"))
		require.ErrorIs(t, err, sv.ErrAppNotStarted)
		results := make(chan error, 2)
		for range 2 {
			go func() {
				results <- e.SealRegistration(t.Context())
			}()
		}
		require.NoError(t, waitChannel(t, results))
		require.NoError(t, waitChannel(t, results))
		h, err := e.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "retry", Workflow: "echo", Input: &api.RunInput{RunID: "retry"}})
		require.NoError(t, err)
		out, err := h.Wait(t.Context())
		require.NoError(t, err)
		assert.Equal(t, "retry", out.RunID)
	})
	t.Run("failed startup retries with registration closed", func(t *testing.T) {
		b := &migrationBarrier{FS: sv.EmbeddedMigrationsFS(), entered: make(chan struct{}), release: make(chan struct{})}
		close(b.release)
		b.fail.Store(true)
		e, err := NewWorker(Options{App: testApp(t, filepath.Join(t.TempDir(), "s.db"), sv.WithMigrationsFS(b))})
		require.NoError(t, err)
		defer func() {
			releaseBarrier(b.release)
			assert.NoError(t, e.Close())
		}()
		registerEcho(t, e)
		require.Error(t, e.SealRegistration(t.Context()))
		require.Error(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "late"}))
		b.fail.Store(false)
		assert.NoError(t, e.SealRegistration(t.Context()))
	})
	t.Run("client ownership", func(t *testing.T) {
		client := testApp(t, filepath.Join(t.TempDir(), "s.db"))
		require.NoError(t, client.Start(t.Context()))
		defer func() {
			assert.NoError(t, client.Shutdown(context.Background()))
		}()
		e, err := NewClient(Options{Client: client})
		require.NoError(t, err)
		assert.NoError(t, e.SealRegistration(t.Context()))
		require.Error(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "x"}))
		require.NoError(t, e.Close())
		_, err = sv.StartWorkflowByName(t.Context(), client, "unregistered", nil, sv.WithInstanceID("still-open"))
		assert.NoError(t, err)
	})
}

func TestStartErrors(t *testing.T) {
	e := newTestEngine(t)
	for _, req := range []engine.WorkflowStartRequest{{Workflow: "echo"}, {ID: "id"}, {ID: "id", Workflow: "echo"}} {
		h, err := e.StartWorkflow(t.Context(), req)
		assert.Nil(t, h)
		require.ErrorIs(t, err, engine.ErrWorkflowStartRejected)
		assert.NotErrorIs(t, err, engine.ErrWorkflowAlreadyStarted)
	}
}

func TestExpiredActivationCannotAcceptAnotherAttempt(t *testing.T) {
	// A's expired wait is delayed until B has become active. The cancellation
	// decision must use A's acceptance, not the current global worker state.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var canceled bool
	first := &activation{done: make(chan struct{}), cancel: func() {
		canceled = true
	}}
	e := &Engine{active: true}
	require.ErrorIs(t, e.cancelActivation(ctx, first), context.Canceled)
	assert.True(t, canceled)
}

func TestCloseDuringActivation(t *testing.T) {
	barrier := &migrationBarrier{FS: sv.EmbeddedMigrationsFS(), entered: make(chan struct{}), release: make(chan struct{})}
	defer releaseBarrier(barrier.release)
	db := filepath.Join(t.TempDir(), "s.db")
	client := testApp(t, db)
	require.NoError(t, client.Start(t.Context()))
	defer func() {
		assert.NoError(t, client.Shutdown(context.Background()))
	}()
	e, err := NewWorker(Options{App: testApp(t, db, sv.WithMigrationsFS(barrier)), Client: client})
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() {
		result <- e.SealRegistration(t.Context())
	}()
	waitChannel(t, barrier.entered)
	closed := make(chan error, 1)
	go func() {
		closed <- e.Close()
	}()
	// A retry cannot activate until the same blocked startup has finished.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	require.Error(t, e.SealRegistration(ctx))
	close(barrier.release)
	require.Error(t, waitChannel(t, result))
	require.NoError(t, waitChannel(t, closed))
	require.Error(t, e.SealRegistration(t.Context()))
	_, err = sv.StartWorkflowByName(t.Context(), client, "unregistered", nil, sv.WithInstanceID("client-survives"))
	assert.NoError(t, err)
}

func TestIncompleteShutdownRejectsReuse(t *testing.T) {
	app := testApp(t, filepath.Join(t.TempDir(), "s.db"), sv.WithShutdownTimeout(10*time.Millisecond))
	e, err := NewWorker(Options{App: app})
	require.NoError(t, err)
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	require.NoError(t, e.RegisterWorkflow(t.Context(), engine.WorkflowDefinition{Name: "blocked", Handler: func(engine.WorkflowContext, *api.RunInput) (*api.RunOutput, error) {
		close(entered)
		<-release
		close(returned)
		return &api.RunOutput{}, nil
	}}))
	require.NoError(t, e.SealRegistration(t.Context()))
	_, err = e.StartWorkflow(t.Context(), engine.WorkflowStartRequest{ID: "blocked", Workflow: "blocked", Input: &api.RunInput{}})
	require.NoError(t, err)
	waitChannel(t, entered)
	cleanupErr := e.Close()
	require.ErrorContains(t, cleanupErr, "cleanup incomplete")
	require.ErrorIs(t, e.SealRegistration(t.Context()), cleanupErr)
	require.ErrorIs(t, e.Close(), cleanupErr)
	close(release)
	waitChannel(t, returned)
}

func TestConstructors(t *testing.T) {
	_, err := NewWorker(Options{})
	require.Error(t, err)
	_, err = NewClient(Options{})
	require.Error(t, err)
	_, err = NewClient(Options{App: sv.NewApp(), Client: sv.NewApp()})
	require.Error(t, err)
}

func releaseBarrier(release chan struct{}) {
	select {
	case <-release:
	default:
		close(release)
	}
}

func TestActivationResultPreservesDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	cleanupErr := errors.New("cleanup did not finish")
	// Startup completed late, and cleanup failed before the canceled caller
	// resumed its select. The caller's result and retained cleanup poison differ.
	pending := &activation{done: make(chan struct{}), err: cleanupErr}
	close(pending.done)
	require.ErrorIs(t, activationResult(ctx, pending), context.DeadlineExceeded)
	assert.Same(t, cleanupErr, pending.err)
	accepted := &activation{done: make(chan struct{}), accepted: true}
	close(accepted.done)
	require.NoError(t, activationResult(ctx, accepted))
	startupErr := errors.New("startup failed")
	failed := &activation{done: make(chan struct{}), err: startupErr}
	close(failed.done)
	assert.ErrorIs(t, activationResult(t.Context(), failed), startupErr)
}
