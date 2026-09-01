package agentfeatures_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	temporalengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	runloginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	sessioninmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	"github.com/stretchr/testify/require"
)

type retryIdempotencyPlanner struct {
	mu       sync.Mutex
	attempts int
	ledger   *fileEffectLedger
}

type silentTemporalLogger struct{}

type fileEffectLedger struct {
	dir string
}

const temporalCLIVersion = "v1.6.1"

func TestGeneratedFeatureRealTemporalContracts(t *testing.T) {
	server, err := testsuite.StartDevServer(t.Context(), testsuite.DevServerOptions{
		CachedDownload: testsuite.CachedDownload{Version: temporalCLIVersion},
		ClientOptions: &client.Options{
			DataConverter: temporalengine.NewAgentDataConverter(nil),
			Logger:        silentTemporalLogger{},
		},
		LogLevel: "error",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		server.Client().Close()
		require.NoError(t, server.Stop())
	})

	t.Run("generated coordinator matches in-memory behavior", func(t *testing.T) {
		ctx, cancel := temporalScenarioContext(t)
		defer cancel()
		eng := newTemporalAcceptanceEngine(t, server.Client())
		fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
		exec := newRecordingWorkflowExecutor()
		runGeneratedFeatureAwaitScenario(
			t,
			ctx,
			fx,
			exec,
			"sess-temporal-core",
			"run-temporal-core",
			1200*time.Millisecond,
			5*time.Second,
			agentsruntime.WithRunTimeBudget(time.Second),
		)
	})

	t.Run("planner retry exposes attempts and commits once", func(t *testing.T) {
		ctx, cancel := temporalScenarioContext(t)
		defer cancel()
		eng := newTemporalAcceptanceEngine(t, server.Client())
		fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
		exec := newRecordingWorkflowExecutor()
		ledger := &fileEffectLedger{dir: t.TempDir()}
		plan := newRetryIdempotencyPlanner(ledger)
		require.NoError(t, coordinator.RegisterUsedToolsets(ctx, fx.rt, coordinator.WithWorkflowExecutor(exec)))
		require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, fx.rt, coordinator.CoordinatorAgentConfig{Planner: plan}))
		_, err := fx.rt.CreateSession(ctx, "sess-temporal-retry")
		require.NoError(t, err)

		out, err := coordinator.NewClient(fx.rt).Run(
			ctx,
			"sess-temporal-retry",
			[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "retry"}}}},
			agentsruntime.WithRunID("run-temporal-retry"),
		)
		require.NoError(t, err)
		require.Equal(t, "retried safely", messageText(out.Final))
		attempts := plan.attemptCount()
		commits, err := ledger.commitCount()
		require.NoError(t, err)
		require.Equal(t, 2, attempts)
		require.Equal(t, 1, commits)
	})

	t.Run("worker replacement replays and resumes durable run", func(t *testing.T) {
		testTemporalWorkerReplacement(t, server.Client())
	})

	t.Run("cancellation terminates generated await", func(t *testing.T) {
		ctx, cancel := temporalScenarioContext(t)
		defer cancel()
		eng := newTemporalAcceptanceEngine(t, server.Client())
		fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
		exec := newRecordingWorkflowExecutor()
		require.NoError(t, coordinator.RegisterUsedToolsets(ctx, fx.rt, coordinator.WithWorkflowExecutor(exec)))
		require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, fx.rt, coordinator.CoordinatorAgentConfig{}))
		_, err := fx.rt.CreateSession(ctx, "sess-temporal-cancel")
		require.NoError(t, err)
		handle, err := coordinator.NewClient(fx.rt).Start(
			ctx,
			"sess-temporal-cancel",
			[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "cancel"}}}},
			agentsruntime.WithRunID("run-temporal-cancel"),
		)
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			return fx.recorder.count(hooks.AwaitTypedInput) == 1
		}, 5*time.Second, 20*time.Millisecond)
		require.NoError(t, fx.rt.CancelRun(ctx, "run-temporal-cancel"))
		_, err = handle.Wait(ctx)
		require.Error(t, err)
		run, err := fx.rt.SessionStore.LoadRun(ctx, "run-temporal-cancel")
		require.NoError(t, err)
		require.Equal(t, session.RunStatusCanceled, run.Status)
	})
}

func TestFileEffectLedger(t *testing.T) {
	t.Run("concurrent commits publish once", func(t *testing.T) {
		ledger := &fileEffectLedger{dir: t.TempDir()}
		const callers = 16
		results := make(chan bool, callers)
		errs := make(chan error, callers)
		var wg sync.WaitGroup
		for range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				committed, err := ledger.commit("run-concurrent")
				results <- committed
				errs <- err
			}()
		}
		wg.Wait()
		close(results)
		close(errs)

		for err := range errs {
			require.NoError(t, err)
		}
		commits := 0
		for committed := range results {
			if committed {
				commits++
			}
		}
		require.Equal(t, 1, commits)
		count, err := ledger.commitCount()
		require.NoError(t, err)
		require.Equal(t, 1, count)
	})

	t.Run("failed commit leaves no marker", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		ledger := &fileEffectLedger{dir: dir}
		committed, err := ledger.commit("run-failed")
		require.Error(t, err)
		require.False(t, committed)
		_, err = os.Stat(dir)
		require.ErrorIs(t, err, os.ErrNotExist)
	})
}

func newTemporalAcceptanceEngine(t *testing.T, temporalClient client.Client) *temporalengine.Engine {
	t.Helper()
	eng, err := temporalengine.NewWorker(temporalengine.Options{
		Client: temporalClient,
		WorkerOptions: temporalengine.WorkerOptions{
			TaskQueue: "agent-features-acceptance",
		},
		Instrumentation: temporalengine.InstrumentationOptions{
			DisableTracing: true,
			DisableMetrics: true,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, eng.Close())
	})
	return eng
}

func newRetryIdempotencyPlanner(ledger *fileEffectLedger) *retryIdempotencyPlanner {
	return &retryIdempotencyPlanner{ledger: ledger}
}

func temporalScenarioContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(t.Context(), 20*time.Second)
}

func testTemporalWorkerReplacement(t *testing.T, temporalClient client.Client) {
	t.Helper()
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	sessions := sessioninmem.New()
	runlog := runloginmem.New()
	firstExec := newRecordingWorkflowExecutor()

	firstEngine, err := temporalengine.NewWorker(temporalengine.Options{
		Client: temporalClient,
		WorkerOptions: temporalengine.WorkerOptions{
			TaskQueue: "agent-features-acceptance",
		},
		Instrumentation: temporalengine.InstrumentationOptions{
			DisableTracing: true,
			DisableMetrics: true,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, firstEngine.Close())
	})
	first := newFeatureRuntime(
		t,
		agentsruntime.WithEngine(firstEngine),
		agentsruntime.WithSessionStore(sessions),
		agentsruntime.WithRunEventStore(runlog),
	)
	require.NoError(t, coordinator.RegisterUsedToolsets(ctx, first.rt, coordinator.WithWorkflowExecutor(firstExec)))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, first.rt, coordinator.CoordinatorAgentConfig{}))
	_, err = first.rt.CreateSession(ctx, "sess-temporal-restart")
	require.NoError(t, err)
	handle, err := coordinator.NewClient(first.rt).Start(
		ctx,
		"sess-temporal-restart",
		[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "restart"}}}},
		agentsruntime.WithRunID("run-temporal-restart"),
	)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return first.recorder.count(hooks.AwaitTypedInput) == 1
	}, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, firstEngine.Close())

	secondEngine := newTemporalAcceptanceEngine(t, temporalClient)
	secondExec := newRecordingWorkflowExecutor()
	second := newFeatureRuntime(
		t,
		agentsruntime.WithEngine(secondEngine),
		agentsruntime.WithSessionStore(sessions),
		agentsruntime.WithRunEventStore(runlog),
	)
	require.NoError(t, coordinator.RegisterUsedToolsets(ctx, second.rt, coordinator.WithWorkflowExecutor(secondExec)))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, second.rt, coordinator.CoordinatorAgentConfig{}))
	require.NoError(t, second.rt.Seal(ctx))
	require.NoError(t, second.rt.ProvideTypedInput(ctx, typedInputAnswer("run-temporal-restart")))
	out, err := handle.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, "generated workflow complete", messageText(out.Final))

	run, err := sessions.LoadRun(ctx, "run-temporal-restart")
	require.NoError(t, err)
	require.Equal(t, session.RunStatusCompleted, run.Status)
	page, err := runlog.List(ctx, "run-temporal-restart", "", 100)
	require.NoError(t, err)
	require.NotEmpty(t, page.Events)
	keys := make(map[string]struct{}, len(page.Events))
	for _, event := range page.Events {
		_, exists := keys[event.EventKey]
		require.False(t, exists, event.EventKey)
		keys[event.EventKey] = struct{}{}
	}
}

func typedInputAnswer(runID string) *api.TypedInputAnswer {
	return &api.TypedInputAnswer{
		RunID:   runID,
		ID:      "approval",
		Payload: rawjson.Message([]byte(`{"content-type":"application/json"}`)),
	}
}

func (l *fileEffectLedger) commit(key string) (bool, error) {
	digest := sha256.Sum256([]byte(key))
	path := filepath.Join(l.dir, hex.EncodeToString(digest[:])+".commit")
	err := os.Mkdir(path, 0o700)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (l *fileEffectLedger) commitCount() (int, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

func (p *retryIdempotencyPlanner) PlanStart(_ context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	p.mu.Lock()
	p.attempts++
	attempt := p.attempts
	p.mu.Unlock()
	if _, err := p.ledger.commit(input.RunContext.RunID); err != nil {
		return nil, err
	}
	if attempt == 1 {
		return nil, errors.New("late planner failure")
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{
		Message: &model.Message{
			Role:  model.ConversationRoleAssistant,
			Parts: []model.Part{model.TextPart{Text: "retried safely"}},
		},
	}}, nil
}

func (p *retryIdempotencyPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, errors.New("unexpected planner resume")
}

func (p *retryIdempotencyPlanner) attemptCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.attempts
}

func (silentTemporalLogger) Debug(string, ...any) {}
func (silentTemporalLogger) Info(string, ...any)  {}
func (silentTemporalLogger) Warn(string, ...any)  {}
func (silentTemporalLogger) Error(string, ...any) {}
