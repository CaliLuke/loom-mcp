package agentfeatures_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	selvedgeengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/selvedge"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	sv "github.com/CaliLuke/selvedge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelvedgeRecordWorkflowHistory(t *testing.T) {
	if os.Getenv("LOOM_MCP_RECORD_WORKFLOW_HISTORY") != "1" {
		t.Skip("set LOOM_MCP_RECORD_WORKFLOW_HISTORY=1 to re-record")
	}
	t.Run("default", func(t *testing.T) {
		recordSelvedgeDefaultHistory(t, false)
	})
	t.Run("nontext resident", func(t *testing.T) {
		recordSelvedgeDefaultHistory(t, true)
	})
	t.Run("startup", recordSelvedgeStartupHistory)
	t.Run("main", recordSelvedgeWorkflowHistory)
	t.Run("child", recordSelvedgeChildWorkflowHistory)
	t.Run("continuation", recordSelvedgeContinuationWorkflowHistory)
	t.Run("multimodal", recordSelvedgeMultimodalWorkflowHistory)
	t.Run("summary", recordSelvedgeSummaryWorkflowHistory)
	t.Run("pause", recordSelvedgePauseWorkflowHistory)
	t.Run("budget", recordSelvedgeBudgetWorkflowHistory)
	t.Run("model call budget", recordSelvedgeModelCallWorkflowHistory)
}

func recordSelvedgeWorkflowHistory(t *testing.T) {
	t.Helper()
	ctx, cancel := selvedgeScenarioContext(t)
	defer cancel()
	eng, observation := newSelvedgeRecordingEngine(t)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng), historyConfirmation(), agentsruntime.WithContextCompaction(coordinator.AgentID, agentsruntime.ContextCompaction{}), agentsruntime.WithContextLimits(map[string]model.ContextLimits{"history": {ContextWindow: 6000}}))
	registerHistoryReplayAgent(t, fx)
	_, err := fx.rt.CreateSession(ctx, "sess-protocol-history")
	require.NoError(t, err)
	handle, err := coordinator.NewClient(fx.rt).Start(ctx, "sess-protocol-history", []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "record this request"}}}, {Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "earlier answer"}}}, {Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: strings.Repeat("earlier history ", 2000)}}}, {Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "recent answer"}}}, {Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "continue"}}}}, agentsruntime.WithRunID("run-protocol-history"), agentsruntime.WithContextOverrides(agentsruntime.ContextOverrides{ContextWindow: new(5000), RecallDiscoveryCalls: new(3), RecallReadCalls: new(3)}))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return fx.recorder.count(hooks.AwaitConfirmation) == 1 && fx.recorder.count(hooks.AwaitClarification) == 1
	}, 30*time.Second, 10*time.Millisecond)
	require.NoError(t, fx.rt.ProvideConfirmation(ctx, &api.ConfirmationDecision{RunID: "run-protocol-history", Approved: true, RequestedBy: "history-recorder"}))
	require.NoError(t, fx.rt.ProvideClarification(ctx, &api.ClarificationAnswer{RunID: "run-protocol-history", ID: "history-clarification", Answer: workflowHistoryAnswer}))
	out, err := handle.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, "history replay complete", messageText(out.Final))
	recordCompletedSelvedgeHistory(t, ctx, eng, observation, out.RunID, workflowHistoryFile)
}

func recordSelvedgeChildWorkflowHistory(t *testing.T) {
	t.Helper()
	ctx, cancel := selvedgeScenarioContext(t)
	defer cancel()
	eng, observation := newSelvedgeRecordingEngine(t)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
	runGeneratedChildScenario(t, ctx, fx, agentsruntime.WithContextOverrides(agentsruntime.ContextOverrides{ContextWindow: new(8000)}))
	recordCompletedSelvedgeHistory(t, ctx, eng, observation, "run-parent-child", workflowChildHistoryFile)
	child := fx.recorder.childRunID("run-parent-child")
	require.NotEmpty(t, child)
	recordCompletedSelvedgeHistory(t, ctx, eng, observation, child, workflowChildRunHistoryFile)
}

func recordSelvedgeContinuationWorkflowHistory(t *testing.T) {
	t.Helper()
	ctx, cancel := selvedgeScenarioContext(t)
	defer cancel()
	eng, observation := newSelvedgeRecordingEngine(t)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng), agentsruntime.WithContextCompaction(coordinator.AgentID, agentsruntime.ContextCompaction{}), agentsruntime.WithContextLimits(map[string]model.ContextLimits{"history": {ContextWindow: 8000}}))
	registerContinuationHistoryAgent(t, fx)
	_, err := fx.rt.CreateSession(ctx, "continuation-history")
	require.NoError(t, err)
	user := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "choose"}}}
	handle, err := coordinator.NewClient(fx.rt).Start(ctx, "continuation-history", []*model.Message{user}, agentsruntime.WithRunID("continuation-first"))
	require.NoError(t, err)
	first, err := handle.Wait(ctx)
	require.NoError(t, err)
	messages := []*model.Message{user, first.Final, {Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "continue"}}}}
	handle, err = coordinator.NewClient(fx.rt).Start(ctx, "continuation-history", messages, agentsruntime.WithRunID("continuation-second"))
	require.NoError(t, err)
	second, err := handle.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, "carry this decision", messageText(second.Final))
	recordCompletedSelvedgeHistory(t, ctx, eng, observation, second.RunID, workflowContinuationHistoryFile)
}

func recordSelvedgeMultimodalWorkflowHistory(t *testing.T) {
	t.Helper()
	ctx, cancel := selvedgeScenarioContext(t)
	defer cancel()
	eng, observation := newSelvedgeRecordingEngine(t)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng), agentsruntime.WithContextCompaction(coordinator.AgentID, agentsruntime.ContextCompaction{KeepRecentTokens: 100, ProtectRecentToolTokens: 2000, MinSavingTokens: 1}), agentsruntime.WithContextLimits(map[string]model.ContextLimits{"history": {ContextWindow: 12000}}))
	registerMultimodalHistoryAgent(t, fx)
	_, err := fx.rt.CreateSession(ctx, "multimodal-session")
	require.NoError(t, err)
	seed := []*model.Message{
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "remember image"}, model.ImagePart{Format: model.ImageFormatPNG, Bytes: []byte{1, 2, 3}}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "noted"}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "remember PDF"}, model.DocumentPart{Name: "manual", Format: model.DocumentFormatPDF, Bytes: []byte("%PDF-test")}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: strings.Repeat("history ", 12000)}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "recall both"}}},
	}
	handle, err := coordinator.NewClient(fx.rt).Start(ctx, "multimodal-session", seed, agentsruntime.WithRunID("multimodal-run"))
	require.NoError(t, err)
	out, err := handle.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, "recalled image and PDF", messageText(out.Final))
	recordCompletedSelvedgeHistory(t, ctx, eng, observation, out.RunID, workflowMultimodalHistoryFile)
}

func recordSelvedgeSummaryWorkflowHistory(t *testing.T) {
	t.Helper()
	ctx, cancel := selvedgeScenarioContext(t)
	defer cancel()
	eng, observation := newSelvedgeRecordingEngine(t)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng), agentsruntime.WithContextCompaction(coordinator.AgentID, agentsruntime.ContextCompaction{TriggerAt: contextmeasure.Rate{Tokens: 1, Bytes: 100}, KeepRecentTokens: 30, MinSavingTokens: 20, ContextModel: &agentsruntime.ContextModel{ID: "summary"}}), agentsruntime.WithContextLimits(map[string]model.ContextLimits{"history": {ContextWindow: 6000}, "summary": {ContextWindow: 200000}}))
	registerSummaryHistoryAgent(t, fx)
	_, err := fx.rt.CreateSession(ctx, "sess-summary-history")
	require.NoError(t, err)
	messages := []*model.Message{
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "remember the task"}}},
		{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: strings.Repeat("recorded evidence ", 500)}}},
		{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "continue"}}},
	}
	handle, err := coordinator.NewClient(fx.rt).Start(ctx, "sess-summary-history", messages, agentsruntime.WithRunID("run-summary-history"), agentsruntime.WithContextOverrides(agentsruntime.ContextOverrides{ContextWindow: new(5000), MaxSummaryChunks: new(2)}))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return fx.recorder.count(hooks.AwaitClarification) == 1
	}, 30*time.Second, 10*time.Millisecond)
	require.NoError(t, fx.rt.ProvideClarification(ctx, &api.ClarificationAnswer{RunID: "run-summary-history", ID: "summary-question", Answer: "yes"}))
	out, err := handle.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, "summary replay complete", messageText(out.Final))
	recordCompletedSelvedgeHistory(t, ctx, eng, observation, out.RunID, workflowSummaryHistoryFile)
}

func recordSelvedgePauseWorkflowHistory(t *testing.T) {
	t.Helper()
	ctx, cancel := selvedgeScenarioContext(t)
	defer cancel()
	eng, observation := newSelvedgeRecordingEngine(t)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng), historyConfirmation())
	registerPauseHistoryAgent(t, fx)
	_, err := fx.rt.CreateSession(ctx, "sess-pause-history")
	require.NoError(t, err)
	handle, err := coordinator.NewClient(fx.rt).Start(ctx, "sess-pause-history", []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "draft then ask"}}}}, agentsruntime.WithRunID("run-pause-history"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return fx.recorder.count(hooks.AwaitConfirmation) == 1
	}, 30*time.Second, 10*time.Millisecond)
	assert.Zero(t, fx.recorder.count(hooks.AwaitClarification))
	require.NoError(t, fx.rt.ProvideConfirmation(ctx, &api.ConfirmationDecision{RunID: "run-pause-history", Approved: true, RequestedBy: "history-recorder"}))
	require.Eventually(t, func() bool {
		return fx.recorder.count(hooks.AwaitClarification) == 1
	}, 30*time.Second, 10*time.Millisecond)
	require.NoError(t, fx.rt.ProvideClarification(ctx, &api.ClarificationAnswer{RunID: "run-pause-history", ID: "confirmed-pause", Answer: workflowHistoryAnswer}))
	out, err := handle.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, "pause replay complete", messageText(out.Final))
	recordCompletedSelvedgeHistory(t, ctx, eng, observation, out.RunID, workflowPauseHistoryFile)
}

func recordSelvedgeBudgetWorkflowHistory(t *testing.T) {
	t.Helper()
	ctx, cancel := selvedgeScenarioContext(t)
	defer cancel()
	eng, observation := newSelvedgeRecordingEngine(t)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng), historyBudgetAdoption(t))
	runGeneratedExternalAwaitScenario(t, ctx, fx, historyTimeBudget)
	recordCompletedSelvedgeHistory(t, ctx, eng, observation, "run-external-await", workflowBudgetHistoryFile)
}

func newSelvedgeRecordingEngine(t *testing.T) (*selvedgeengine.Engine, *selvedgeObservations) {
	t.Helper()
	observations := &selvedgeObservations{}
	app := newSelvedgeAcceptanceApp(filepath.Join(t.TempDir(), "s.db"), sv.WithHooks(observations))
	eng, err := selvedgeengine.NewWorker(selvedgeengine.Options{App: app})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, eng.Close())
	})
	return eng, observations
}

// recordCompletedSelvedgeHistory exports only real terminal executions. Inputs observed
// live are checked separately; the exported format does not contain activity inputs.
func recordCompletedSelvedgeHistory(t *testing.T, ctx context.Context, eng *selvedgeengine.Engine, observations *selvedgeObservations, id, temporalFilename string) {
	t.Helper()
	recordTerminalSelvedgeHistory(t, ctx, eng, observations, id, temporalFilename, sv.StatusCompleted)
}

// recordTerminalSelvedgeHistory preserves the terminal state of a real execution.
func recordTerminalSelvedgeHistory(t *testing.T, ctx context.Context, eng *selvedgeengine.Engine, observations *selvedgeObservations, id, temporalFilename string, terminal sv.WorkflowStatus) {
	t.Helper()
	observations.mu.Lock()
	failures := append([]error(nil), observations.failures...)
	observations.mu.Unlock()
	require.Empty(t, failures)
	history, err := eng.ExportHistory(ctx, id)
	require.NoError(t, err)
	require.Equal(t, terminal, history.Instance.Status)
	require.Equal(t, "selvedge.history/v1", history.Format)
	require.NotEmpty(t, history.Events)
	data, err := json.MarshalIndent(history, "", "  ")
	require.NoError(t, err)
	parsed, err := sv.ParseHistory(data)
	require.NoError(t, err)
	filename := filepath.Join("testdata", "selvedge", filepath.Base(temporalFilename))
	if temporalFilename == workflowModelCallHistoryFile {
		assertSelvedgeModelCallObservations(t, observations.snapshot(id))
	} else {
		assertSelvedgeObservedHistory(t, filepath.Base(filename), parsed, observations.snapshot(id))
	}
	if t.Failed() {
		return
	}
	before := observations.hookCalls()
	require.NoError(t, eng.ReplayHistory(ctx, parsed))
	require.Equal(t, before, observations.hookCalls(), "replay executes no activities or hooks")
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o750))
	require.NoError(t, os.WriteFile(filename, append(data, '\n'), 0o600))
}
