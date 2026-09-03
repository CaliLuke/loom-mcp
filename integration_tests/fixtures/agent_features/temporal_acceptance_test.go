package agentfeatures_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"example.com/agentfeatures/gen/features/toolsets/workflow"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	temporalengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog"
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

type signalTimeoutPlanner struct{}
type terminalCapPlanner struct{}

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
			2500*time.Millisecond,
			8*time.Second,
			agentsruntime.WithRunTimeBudget(2*time.Second),
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
		registerCoordinatorToolsets(t, ctx, fx.rt, exec)
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

	t.Run("generated model recovery survives activity boundaries", func(t *testing.T) {
		ctx, cancel := temporalScenarioContext(t)
		defer cancel()
		eng := newTemporalAcceptanceEngine(t, server.Client())
		runGeneratedModelRecoveryScenario(
			t,
			ctx,
			newFeatureRuntime(t, agentsruntime.WithEngine(eng)),
			"sess-temporal-recovery",
			"run-temporal-recovery",
		)
	})

	t.Run("worker replacement replays and resumes durable run", func(t *testing.T) {
		sessions := sessioninmem.New()
		runEvents := runloginmem.New()
		testTemporalWorkerReplacement(
			t,
			server.Client(),
			sessions,
			runEvents,
			func() (session.Store, runlog.Store) {
				return sessions, runEvents
			},
			func() {},
		)
	})

	t.Run("typed input races workflow timeout without stranding state", func(t *testing.T) {
		testTemporalSignalTimeoutRace(t, server.Client())
	})

	t.Run("child agent links every observable layer", func(t *testing.T) {
		ctx, cancel := temporalScenarioContext(t)
		defer cancel()
		eng := newTemporalAcceptanceEngine(t, server.Client())
		runGeneratedChildScenario(t, ctx, newFeatureRuntime(t, agentsruntime.WithEngine(eng)))
	})

	t.Run("clarification and external result share one barrier", func(t *testing.T) {
		ctx, cancel := temporalScenarioContext(t)
		defer cancel()
		eng := newTemporalAcceptanceEngine(t, server.Client())
		runGeneratedExternalAwaitScenario(t, ctx, newFeatureRuntime(t, agentsruntime.WithEngine(eng)))
	})

	t.Run("parent cancellation reaches child agent", func(t *testing.T) {
		ctx, cancel := temporalScenarioContext(t)
		defer cancel()
		eng := newTemporalAcceptanceEngine(t, server.Client())
		runGeneratedChildCancellationScenario(t, ctx, newFeatureRuntime(t, agentsruntime.WithEngine(eng)))
	})

	t.Run("cancellation terminates generated await", func(t *testing.T) {
		ctx, cancel := temporalScenarioContext(t)
		defer cancel()
		eng := newTemporalAcceptanceEngine(t, server.Client())
		fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
		exec := newRecordingWorkflowExecutor()
		registerCoordinatorToolsets(t, ctx, fx.rt, exec)
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
			return fx.recorder.count(hooks.AwaitTypedInput) > 0
		}, 5*time.Second, 20*time.Millisecond)
		require.NoError(t, fx.rt.CancelRun(ctx, "run-temporal-cancel"))
		_, err = handle.Wait(ctx)
		require.Error(t, err)
		run, err := fx.rt.SessionStore.LoadRun(ctx, "run-temporal-cancel")
		require.NoError(t, err)
		require.Equal(t, session.RunStatusCanceled, run.Status)
	})
	t.Run("fixed terminal plan crosses Temporal boundaries", func(t *testing.T) {
		ctx, cancel := temporalScenarioContext(t)
		defer cancel()
		eng := newTemporalAcceptanceEngine(t, server.Client())
		fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
		exec := newRecordingWorkflowExecutor()
		registerCoordinatorToolsets(t, ctx, fx.rt, exec)
		require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, fx.rt, coordinator.CoordinatorAgentConfig{Planner: terminalCapPlanner{}}))
		_, err := fx.rt.CreateSession(ctx, "sess-temporal-terminal")
		require.NoError(t, err)

		terminalCall := func(reason string) agentsruntime.LimitTerminalCall {
			return agentsruntime.LimitTerminalCall{
				Name: workflow.Finalize, Payload: rawjson.Message(fmt.Sprintf(`{"reason":%q}`, reason)),
			}
		}
		out, err := coordinator.NewClient(fx.rt).Run(
			ctx,
			"sess-temporal-terminal",
			[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "finish at the cap"}}}},
			agentsruntime.WithRunID("run-temporal-terminal"),
			agentsruntime.WithRunMaxToolCalls(1),
			agentsruntime.WithLimitTerminalPlans(agentsruntime.LimitTerminalPlans{
				TimeBudget:  terminalCall("time_budget"),
				ToolCallCap: terminalCall("tool_call_cap"),
				RecoveryCap: terminalCall("recovery_cap"),
			}),
		)
		require.NoError(t, err)
		require.NotNil(t, out.FinalToolResult)
		require.Equal(t, workflow.Finalize, out.FinalToolResult.Name)
		calls := exec.callsSnapshot()
		require.Len(t, calls, 1)
		require.Equal(t, workflow.Finalize, calls[0].Name)
		require.NotEmpty(t, calls[0].ToolCallID)
		require.Equal(t, string(planner.TerminationReasonToolCap), calls[0].Labels[agentsruntime.FinalizationReasonLabel])
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
	return context.WithTimeout(t.Context(), 45*time.Second)
}

func testTemporalWorkerReplacement(
	t *testing.T,
	temporalClient client.Client,
	firstSessions session.Store,
	firstRunEvents runlog.Store,
	replacementStores func() (session.Store, runlog.Store),
	afterFirstWorker func(),
) {
	t.Helper()
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
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
		agentsruntime.WithSessionStore(firstSessions),
		agentsruntime.WithRunEventStore(firstRunEvents),
	)
	registerCoordinatorToolsets(t, ctx, first.rt, firstExec)
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
		return first.recorder.count(hooks.AwaitTypedInput) > 0
	}, 5*time.Second, 20*time.Millisecond)
	firstPage, err := firstRunEvents.List(ctx, "run-temporal-restart", "", 100)
	require.NoError(t, err)
	require.NotEmpty(t, firstPage.Events)
	preReplacementKeys := make(map[string]struct{}, len(firstPage.Events))
	for _, event := range firstPage.Events {
		preReplacementKeys[event.EventKey] = struct{}{}
	}
	require.NoError(t, firstEngine.Close())
	afterFirstWorker()
	secondSessions, secondRunEvents := replacementStores()

	secondEngine := newTemporalAcceptanceEngine(t, temporalClient)
	secondExec := newRecordingWorkflowExecutor()
	second := newFeatureRuntime(
		t,
		agentsruntime.WithEngine(secondEngine),
		agentsruntime.WithSessionStore(secondSessions),
		agentsruntime.WithRunEventStore(secondRunEvents),
	)
	registerCoordinatorToolsets(t, ctx, second.rt, secondExec)
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, second.rt, coordinator.CoordinatorAgentConfig{}))
	require.NoError(t, second.rt.Seal(ctx))
	require.NoError(t, second.rt.ProvideTypedInput(ctx, typedInputAnswer("run-temporal-restart")))
	require.Eventually(t, func() bool {
		return second.recorder.count(hooks.AwaitConfirmation) > 0
	}, 10*time.Second, 20*time.Millisecond)
	require.NoError(t, second.rt.ProvideConfirmation(ctx, &api.ConfirmationDecision{
		RunID:       "run-temporal-restart",
		Approved:    true,
		RequestedBy: "fixture-reviewer",
	}))
	out, err := handle.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, "generated workflow complete", messageText(out.Final))

	run, err := secondSessions.LoadRun(ctx, "run-temporal-restart")
	require.NoError(t, err)
	require.Equal(t, session.RunStatusCompleted, run.Status)
	page, err := secondRunEvents.List(ctx, "run-temporal-restart", "", 100)
	require.NoError(t, err)
	require.NotEmpty(t, page.Events)
	keyCounts := make(map[string]int, len(page.Events))
	for _, event := range page.Events {
		keyCounts[event.EventKey]++
		require.Equal(t, 1, keyCounts[event.EventKey], event.EventKey)
	}
	for eventKey := range preReplacementKeys {
		require.Equal(t, 1, keyCounts[eventKey], eventKey)
	}
}

func testTemporalSignalTimeoutRace(t *testing.T, temporalClient client.Client) {
	t.Helper()
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	eng := newTemporalAcceptanceEngine(t, temporalClient)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
	registerCoordinatorToolsets(t, ctx, fx.rt, newRecordingWorkflowExecutor())
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, fx.rt, coordinator.CoordinatorAgentConfig{Planner: signalTimeoutPlanner{}}))
	require.NoError(t, fx.rt.Seal(ctx))

	const workflowTimeout = 2 * time.Second
	offsets := []time.Duration{time.Second, 1900 * time.Millisecond, workflowTimeout + time.Nanosecond}
	completed := 0
	timedOut := 0
	for i, offset := range offsets {
		runID := fmt.Sprintf("run-signal-timeout-%d", i)
		sessionID := fmt.Sprintf("sess-signal-timeout-%d", i)
		_, err := fx.rt.CreateSession(ctx, sessionID)
		require.NoError(t, err)
		startedAt := time.Now()
		handle, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:                 runID,
			TaskQueue:          coordinator.DefaultTaskQueue,
			WorkflowRunTimeout: workflowTimeout,
		}, coordinator.WorkflowName, &agentsruntime.RunInput{
			AgentID:   coordinator.AgentID,
			RunID:     runID,
			SessionID: sessionID,
			Messages:  []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "race"}}}},
		})
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			return fx.recorder.countForRun(hooks.AwaitTypedInput, runID) == 1
		}, time.Second, 10*time.Millisecond)
		var out *api.RunOutput
		var waitErr error
		var signalErr error
		if offset > workflowTimeout {
			waitErr = handle.Get(ctx, &out)
			signalErr = temporalClient.SignalWorkflow(ctx, runID, "", api.SignalProvideTypedInput, &api.TypedInputAnswer{
				RunID:   runID,
				ID:      "signal-race",
				Payload: rawjson.Message([]byte(`{"ok":true}`)),
			})
		} else {
			if remaining := offset - time.Since(startedAt); remaining > 0 {
				timer := time.NewTimer(remaining)
				select {
				case <-ctx.Done():
					timer.Stop()
					require.NoError(t, ctx.Err())
				case <-timer.C:
				}
			}
			signalErr = temporalClient.SignalWorkflow(ctx, runID, "", api.SignalProvideTypedInput, &api.TypedInputAnswer{
				RunID:   runID,
				ID:      "signal-race",
				Payload: rawjson.Message([]byte(`{"ok":true}`)),
			})
			waitErr = handle.Get(ctx, &out)
		}
		status, statusErr := eng.QueryRunStatus(ctx, runID)
		require.NoError(t, statusErr)
		switch status {
		case engine.RunStatusCompleted:
			require.NoError(t, signalErr)
			require.NoError(t, waitErr)
			require.Equal(t, "signal won", messageText(out.Final))
			completed++
		case engine.RunStatusTimedOut:
			require.Error(t, waitErr)
			timedOut++
		default:
			require.Failf(t, "unexpected terminal status", "run %s ended as %s (signal error: %v, wait error: %v)", runID, status, signalErr, waitErr)
		}
		snapshot, err := fx.rt.GetRunSnapshot(ctx, runID)
		require.NoError(t, err)
		runMeta, err := fx.rt.SessionStore.LoadRun(ctx, runID)
		require.NoError(t, err)
		// Workflow completion and the server-enforced timeout are separate
		// durable commits at this boundary. If the runtime commits completion
		// first, that monotonic terminal result remains authoritative even when
		// Temporal records the workflow as timed out immediately afterward.
		switch snapshot.Status {
		case run.StatusCompleted:
			require.Contains(t, []engine.RunStatus{engine.RunStatusCompleted, engine.RunStatusTimedOut}, status)
			require.Equal(t, session.RunStatusCompleted, runMeta.Status)
		case run.StatusFailed:
			require.Equal(t, engine.RunStatusTimedOut, status)
			require.Equal(t, session.RunStatusFailed, runMeta.Status)
		default:
			require.Failf(t, "stranded canonical run status", "run %s remained %s after engine terminal status %s", runID, snapshot.Status, status)
		}
	}
	require.GreaterOrEqual(t, completed, 1)
	require.GreaterOrEqual(t, timedOut, 1)
}

func (signalTimeoutPlanner) PlanStart(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{Await: planner.NewAwait(
		planner.AwaitTypedInputItem(&planner.AwaitTypedInput{
			ID:     "signal-race",
			Title:  "Signal race",
			Schema: rawjson.Message([]byte(`{"type":"object"}`)),
		}),
	)}, nil
}

func (signalTimeoutPlanner) PlanResume(_ context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	if input == nil || len(input.TypedInputs) != 1 {
		return nil, errors.New("signal-timeout planner expected one typed input")
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
		Role:  model.ConversationRoleAssistant,
		Parts: []model.Part{model.TextPart{Text: "signal won"}},
	}}}, nil
}

func (terminalCapPlanner) PlanStart(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{
		{Name: workflow.Draft, Payload: rawjson.Message(`{"topic":"loom"}`)},
		{Name: workflow.Review, Payload: rawjson.Message(`{"strict":true}`)},
	}}, nil
}

func (terminalCapPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, errors.New("unexpected terminal-cap planner resume")
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
