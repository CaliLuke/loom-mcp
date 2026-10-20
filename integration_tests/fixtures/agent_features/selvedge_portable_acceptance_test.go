package agentfeatures_test

import (
	"context"
	"example.com/agentfeatures/gen/features/agents/coordinator"
	"example.com/agentfeatures/gen/features/toolsets/workflow"
	"fmt"
	"github.com/CaliLuke/loom-mcp/v2/features/model/openai"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	selvedgeengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/selvedge"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	runloginmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runlog/inmem"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	sessioninmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/session/inmem"
	sv "github.com/CaliLuke/selvedge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSelvedgePortableAcceptance(t *testing.T) {
	t.Run("planner retry exposes attempts and commits once", func(t *testing.T) {
		ctx, cancel := selvedgeScenarioContext(t)
		defer cancel()
		eng := newSelvedgeAcceptanceEngine(t, filepath.Join(t.TempDir(), "s.db"))
		fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
		exec := newRecordingWorkflowExecutor()
		ledger := &fileEffectLedger{dir: t.TempDir()}
		plan := newRetryIdempotencyPlanner(ledger)
		registerCoordinatorToolsets(t, ctx, fx.rt, exec)
		require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, fx.rt, coordinator.CoordinatorAgentConfig{Planner: plan}))
		_, err := fx.rt.CreateSession(ctx, "sess-selvedge-retry")
		require.NoError(t, err)

		out, err := coordinator.NewClient(fx.rt).Run(
			ctx,
			"sess-selvedge-retry",
			[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "retry"}}}},
			agentsruntime.WithRunID("run-selvedge-retry"),
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
		ctx, cancel := selvedgeScenarioContext(t)
		defer cancel()
		eng := newSelvedgeAcceptanceEngine(t, filepath.Join(t.TempDir(), "s.db"))
		runGeneratedModelRecoveryScenario(
			t,
			ctx,
			newFeatureRuntime(t, agentsruntime.WithEngine(eng)),
			"sess-selvedge-recovery",
			"run-selvedge-recovery",
		)
	})

	t.Run("child agent links every observable layer", func(t *testing.T) {
		ctx, cancel := selvedgeScenarioContext(t)
		defer cancel()
		eng := newSelvedgeAcceptanceEngine(t, filepath.Join(t.TempDir(), "s.db"))
		runGeneratedChildScenario(t, ctx, newFeatureRuntime(t, agentsruntime.WithEngine(eng)))
	})

	t.Run("clarification and external result share one barrier", func(t *testing.T) {
		ctx, cancel := selvedgeScenarioContext(t)
		defer cancel()
		eng := newSelvedgeAcceptanceEngine(t, filepath.Join(t.TempDir(), "s.db"))
		// Allow persisted workflow-clock calls without turning this barrier proof into a latency gate.
		runGeneratedExternalAwaitScenario(t, ctx, newFeatureRuntime(t, agentsruntime.WithEngine(eng)), 30*time.Second)
	})

	t.Run("parent cancellation reaches child agent", func(t *testing.T) {
		ctx, cancel := selvedgeScenarioContext(t)
		defer cancel()
		eng := newSelvedgeAcceptanceEngine(t, filepath.Join(t.TempDir(), "s.db"))
		runGeneratedChildCancellationScenario(t, ctx, newFeatureRuntime(t, agentsruntime.WithEngine(eng)))
	})

	t.Run("cancellation terminates generated await", func(t *testing.T) {
		ctx, cancel := selvedgeScenarioContext(t)
		defer cancel()
		eng := newSelvedgeAcceptanceEngine(t, filepath.Join(t.TempDir(), "s.db"))
		fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
		exec := newRecordingWorkflowExecutor()
		registerCoordinatorToolsets(t, ctx, fx.rt, exec)
		require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, fx.rt, coordinator.CoordinatorAgentConfig{}))
		_, err := fx.rt.CreateSession(ctx, "sess-selvedge-cancel")
		require.NoError(t, err)
		awaited := make(chan struct{})
		var awaitOnce sync.Once
		sub, err := fx.rt.Bus.Register(hooks.SubscriberFunc(func(_ context.Context, event hooks.Event) error {
			if event.Type() == hooks.AwaitTypedInput && event.RunID() == "run-selvedge-cancel" {
				awaitOnce.Do(func() {
					close(awaited)
				})
			}
			return nil
		}))
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, sub.Close())
		})
		handle, err := coordinator.NewClient(fx.rt).Start(
			ctx,
			"sess-selvedge-cancel",
			[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "cancel"}}}},
			agentsruntime.WithRunID("run-selvedge-cancel"),
		)
		require.NoError(t, err)
		// Cancellation requires the await barrier, not a startup latency bound.
		// Persisted workflow steps share the scenario's deadline.
		select {
		case <-awaited:
		case <-ctx.Done():
			t.Fatalf("typed-input await was not published: %v", ctx.Err())
		}
		require.NoError(t, fx.rt.CancelRun(ctx, "run-selvedge-cancel"))
		_, err = handle.Wait(ctx)
		require.Error(t, err)
		run, err := fx.rt.SessionStore.LoadRun(ctx, "run-selvedge-cancel")
		require.NoError(t, err)
		require.Equal(t, session.RunStatusCanceled, run.Status)
	})

	t.Run("fixed terminal plan crosses Selvedge boundaries", func(t *testing.T) {
		ctx, cancel := selvedgeScenarioContext(t)
		defer cancel()
		eng := newSelvedgeAcceptanceEngine(t, filepath.Join(t.TempDir(), "s.db"))
		fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
		exec := newRecordingWorkflowExecutor()
		registerCoordinatorToolsets(t, ctx, fx.rt, exec)
		require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, fx.rt, coordinator.CoordinatorAgentConfig{Planner: terminalCapPlanner{}}))
		_, err := fx.rt.CreateSession(ctx, "sess-selvedge-terminal")
		require.NoError(t, err)

		terminalCall := func(reason string) agentsruntime.LimitTerminalCall {
			return agentsruntime.LimitTerminalCall{
				Name: workflow.Finalize, Payload: rawjson.Message(fmt.Sprintf(`{"reason":%q}`, reason)),
			}
		}
		out, err := coordinator.NewClient(fx.rt).Run(
			ctx,
			"sess-selvedge-terminal",
			[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "finish at the cap"}}}},
			agentsruntime.WithRunID("run-selvedge-terminal"),
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
	t.Run("worker replacement replays and resumes durable run", testSelvedgeWorkerReplacement)
	t.Run("multimodal planner fails once", testSelvedgeMultimodalPlannerRetry)
}

func selvedgeScenarioContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(t.Context(), 120*time.Second)
}

func testSelvedgeWorkerReplacement(t *testing.T) {
	t.Helper()
	ctx, cancel := selvedgeScenarioContext(t)
	defer cancel()
	firstExec := newRecordingWorkflowExecutor()
	deadline, ok := ctx.Deadline()
	require.True(t, ok)

	db := filepath.Join(t.TempDir(), "s.db")
	client := sv.NewApp(sv.WithDatabase(db), sv.WithWaitPollInterval(10*time.Millisecond))
	require.NoError(t, client.Start(ctx))
	t.Cleanup(func() {
		require.NoError(t, client.Shutdown(context.Background()))
	})
	firstSessions := sessioninmem.New()
	firstRunEvents := runloginmem.New()
	worker := func() *selvedgeengine.Engine {
		app := newSelvedgeAcceptanceApp(db)
		eng, err := selvedgeengine.NewWorker(selvedgeengine.Options{App: app, Client: client})
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, eng.Close())
		})
		return eng
	}
	firstEngine := worker()
	first := newFeatureRuntime(
		t,
		agentsruntime.WithEngine(firstEngine),
		agentsruntime.WithSessionStore(firstSessions),
		agentsruntime.WithRunEventStore(firstRunEvents),
	)
	registerCoordinatorToolsets(t, ctx, first.rt, firstExec)
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, first.rt, coordinator.CoordinatorAgentConfig{}))
	_, err := first.rt.CreateSession(ctx, "sess-selvedge-restart")
	require.NoError(t, err)
	handle, err := coordinator.NewClient(first.rt).Start(
		ctx,
		"sess-selvedge-restart",
		[]*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "restart"}}}},
		agentsruntime.WithRunID("run-selvedge-restart"),
	)
	require.NoError(t, err)
	// Wait for the committed pause and for all reserved activities to finish.
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		if !assert.Positive(collect, first.recorder.count(hooks.AwaitTypedInput)) || !assert.Positive(collect, first.recorder.count(hooks.RunPaused)) {
			return
		}
		tasks, err := sv.ListActivityTasks(ctx, client, "run-selvedge-restart")
		if !assert.NoError(collect, err) {
			return
		}
		for _, task := range tasks {
			assert.Equal(collect, "completed", task.Status)
		}
	}, time.Until(deadline), 20*time.Millisecond)
	firstPage, err := firstRunEvents.List(ctx, "run-selvedge-restart", "", 100)
	require.NoError(t, err)
	require.NotEmpty(t, firstPage.Events)
	preReplacementKeys := make(map[string]struct{}, len(firstPage.Events))
	for _, event := range firstPage.Events {
		preReplacementKeys[event.EventKey] = struct{}{}
	}
	require.NoError(t, firstEngine.Close())
	secondSessions, secondRunEvents := firstSessions, firstRunEvents

	secondEngine := worker()
	secondExec := newRecordingWorkflowExecutor()
	second := newFeatureRuntime(
		t,
		agentsruntime.WithEngine(secondEngine),
		agentsruntime.WithSessionStore(secondSessions),
		agentsruntime.WithRunEventStore(secondRunEvents),
		// Tool references survive worker replacement in the shared root scope.
		agentsruntime.WithRunStore(first.rt.RunStore),
	)
	registerCoordinatorToolsets(t, ctx, second.rt, secondExec)
	require.NoError(t, coordinator.RegisterCoordinatorAgent(ctx, second.rt, coordinator.CoordinatorAgentConfig{}))
	require.NoError(t, second.rt.Seal(ctx))
	require.NoError(t, second.rt.ProvideTypedInput(ctx, typedInputAnswer("run-selvedge-restart")))
	require.Eventually(t, func() bool {
		return second.recorder.count(hooks.AwaitConfirmation) > 0
	}, time.Until(deadline), 20*time.Millisecond)
	require.NoError(t, second.rt.ProvideConfirmation(ctx, &api.ConfirmationDecision{
		RunID:       "run-selvedge-restart",
		Approved:    true,
		RequestedBy: "fixture-reviewer",
	}))
	out, err := handle.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, "generated workflow complete", messageText(out.Final))
	events, err := agentsruntime.LoadRunToolEvents(ctx, second.rt.RunStore, out.RunID, out.ToolEventsRef)
	require.NoError(t, err)
	require.Len(t, events, out.ToolEventCount)
	require.Contains(t, toolEventCallIDs(events), "draft")

	run, err := secondSessions.LoadRun(ctx, "run-selvedge-restart")
	require.NoError(t, err)
	require.Equal(t, session.RunStatusCompleted, run.Status)
	page, err := secondRunEvents.List(ctx, "run-selvedge-restart", "", 100)
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

func testSelvedgeMultimodalPlannerRetry(t *testing.T) {
	t.Helper()
	ctx, cancel := selvedgeScenarioContext(t)
	defer cancel()
	eng := newSelvedgeAcceptanceEngine(t, filepath.Join(t.TempDir(), "s.db"))
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
	provider, err := model.NewClient(&openai.Client{})
	require.NoError(t, err)
	require.NoError(t, fx.rt.RegisterModel("unsupported", provider))
	p := &unsupportedMultimodalPlanner{}
	require.NoError(t, coordinator.RegisterUsedToolsets(t.Context(), fx.rt, coordinator.WithWorkflowExecutor(newRecordingWorkflowExecutor())))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{Planner: p}))
	_, err = fx.rt.CreateSession(ctx, "unsupported-session")
	require.NoError(t, err)
	seed := []*model.Message{
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "read"}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.ToolUsePart{ID: "call", Name: "read", Input: map[string]any{}}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.ToolResultPart{ToolUseID: "call", Content: "descriptor", Parts: []model.ToolResultContentPart{model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte{1, 2, 3}}}}}},
	}
	handle, err := coordinator.NewClient(fx.rt).Start(ctx, "unsupported-session", seed, agentsruntime.WithRunID("unsupported-multimodal-run"))
	require.NoError(t, err)
	_, err = handle.Wait(ctx)
	require.Error(t, err)
	var application *engine.ApplicationError
	require.ErrorAs(t, err, &application)
	assert.Equal(t, "model.unsupported_part", application.Type)
	assert.True(t, application.NonRetryable)
	assert.Equal(t, int32(1), p.attempts.Load())
}
