package agentfeatures_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	temporalengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
)

const workflowDefaultHistoryFile = "testdata/workflow_protocol_v21_default.json"
const workflowNonTextResidentHistoryFile = "testdata/workflow_protocol_v21_nontext_resident.json"

type defaultHistoryPlanner struct{}
type defaultHistoryClient struct{ modelClientFunc }

func (defaultHistoryClient) ProviderConfiguration(context.Context, *model.Request) string {
	return "fixture-qualified"
}
func (defaultHistoryClient) ToolResultParts(context.Context, *model.Request) model.ToolResultPartSupport {
	return model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{"jpeg"}, MaxImageBytes: 10000}
}
func (defaultHistoryPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	if err := measureHistoryRequest(ctx, in.Agent, in.Messages); err != nil {
		return nil, err
	}
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "workflow.draft", ToolCallID: "draft", Payload: rawjson.Message(`{"topic":"default"}`)}}}, nil
}
func (defaultHistoryPlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	if err := measureHistoryRequest(ctx, in.Agent, in.Messages); err != nil {
		return nil, err
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "qualified default complete"}}}}}, nil
}
func registerDefaultHistoryAgent(t *testing.T, fx *featureRuntime) {
	t.Helper()
	require.NoError(t, fx.rt.RegisterModel("history", defaultHistoryClient{modelClientFunc: func(context.Context, *model.Request) (*model.Response, error) {
		return &model.Response{Content: []model.Message{{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "measured"}}}}, StopReason: "end_turn"}, nil
	}}))
	require.NoError(t, coordinator.RegisterUsedToolsets(t.Context(), fx.rt, coordinator.WithWorkflowExecutor(newRecordingWorkflowExecutor())))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{Planner: defaultHistoryPlanner{}}))
}
func defaultHistoryOptions() []agentsruntime.RuntimeOption {
	return []agentsruntime.RuntimeOption{agentsruntime.WithContextQualification(compaction.Qualification{Config: "fixture-qualified", Models: []string{"history"}, Tier: compaction.Evict}), agentsruntime.WithContextLimits(map[string]model.ContextLimits{"history": {ContextWindow: 12000}})}
}
func runDefaultHistoryScenario(t *testing.T, ctx context.Context, fx *featureRuntime, resident bool) string {
	t.Helper()
	registerDefaultHistoryAgent(t, fx)
	_, err := fx.rt.CreateSession(ctx, "default-session")
	require.NoError(t, err)
	first := &model.Message{Role: "user", Parts: []model.Part{model.TextPart{Text: "remember"}}}
	if resident {
		first.Parts = append(first.Parts, model.ImagePart{Format: "png", Bytes: []byte{1, 2, 3}})
	}
	seed := []*model.Message{first, {Role: "assistant", Parts: []model.Part{model.TextPart{Text: strings.Repeat("earlier context ", 6000)}}}, {Role: "user", Parts: []model.Part{model.TextPart{Text: "finish"}}}}
	runID := "default-run"
	if resident {
		runID = "nontext-resident-run"
	}
	out, err := coordinator.NewClient(fx.rt).Run(ctx, "default-session", seed, agentsruntime.WithRunID(runID))
	require.NoError(t, err)
	require.Equal(t, "qualified default complete", messageText(out.Final))
	record, err := runrecord.Read(ctx, fx.rt.RunStore, fx.rt.RunEventStore, out.RunID, out.RunID)
	require.NoError(t, err)
	require.NotNil(t, record.Runs[0].ContextSettings)
	assert.True(t, record.Runs[0].ContextSettings.Default)
	assert.True(t, record.Runs[0].ContextSettings.KeepNonTextResident)
	assert.False(t, record.Runs[0].ContextSettings.Threshold)
	for _, call := range record.Runs[0].Calls {
		if call.Completion != nil && call.Completion.Facts != nil {
			assert.Equal(t, "fixture-qualified", call.Completion.Facts.ProviderConfig)
			assert.Equal(t, compaction.Evict, call.Completion.Facts.Qualified)
		}
	}
	if resident {
		assert.Equal(t, 1, fx.recorder.count(hooks.ContextNonTextResident))
		assert.Equal(t, 1, fx.recorder.count(hooks.ContextResidencyAdopted))
		assert.Equal(t, 1, fx.recorder.count(hooks.ContextCompatibility))
		assert.Zero(t, fx.recorder.count(hooks.ContextCompacted))
	} else {
		assert.Positive(t, fx.recorder.count(hooks.ContextCompacted))
		assert.Zero(t, fx.recorder.count(hooks.ContextNonTextResident))
		assert.Zero(t, fx.recorder.count(hooks.ContextResidencyAdopted))
	}
	head, err := fx.rt.SessionStore.(session.ContinuationStore).LoadContinuationHead(ctx, "default-session")
	require.NoError(t, err)
	if resident {
		assert.Nil(t, head)
	} else {
		require.NotNil(t, head)
		assert.Equal(t, out.RunID, head.ID)
	}
	encoded, err := record.Export().Marshal()
	require.NoError(t, err)
	restored, err := runrecord.FromExport(encoded)
	require.NoError(t, err)
	assert.Equal(t, record, restored)
	require.False(t, t.Failed(), "invalid context proof must not be recorded")
	return out.RunID
}
func TestGeneratedDefaultContextAcceptance(t *testing.T) {
	for _, resident := range []bool{false, true} {
		name := "qualified"
		if resident {
			name = "nontext resident"
		}
		t.Run(name, func(t *testing.T) {
			fx := newFeatureRuntime(t, defaultHistoryOptions()...)
			runDefaultHistoryScenario(t, t.Context(), fx, resident)
		})
	}
}
func TestDefaultWorkflowProtocolBoundary(t *testing.T) {
	assert.Equal(t, 21, api.WorkflowProtocolVersion)
	assert.Equal(t, 5, index.TemplateProtocolVersion)
}

func TestGeneratedDefaultContinuationKeepsGoodHead(t *testing.T) {
	fx := newFeatureRuntime(t, defaultHistoryOptions()...)
	first := runDefaultHistoryScenario(t, t.Context(), fx, false)
	second := newFeatureRuntime(t, agentsruntime.WithSessionStore(fx.rt.SessionStore), agentsruntime.WithRunStore(fx.rt.RunStore), agentsruntime.WithRunEventStore(fx.rt.RunEventStore), agentsruntime.WithContextLimits(map[string]model.ContextLimits{"history": {ContextWindow: 12000}}))
	require.NoError(t, coordinator.RegisterUsedToolsets(t.Context(), second.rt, coordinator.WithWorkflowExecutor(newRecordingWorkflowExecutor())))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), second.rt, coordinator.CoordinatorAgentConfig{Planner: defaultHistoryPlanner{}}))
	require.NoError(t, second.rt.RegisterModel("history", modelClientFunc(func(context.Context, *model.Request) (*model.Response, error) {
		return &model.Response{Content: []model.Message{{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "unqualified"}}}}, StopReason: "end_turn"}, nil
	})))
	out, err := coordinator.NewClient(second.rt).Run(t.Context(), "default-session", []*model.Message{{Role: "user", Parts: []model.Part{model.TextPart{Text: "next turn"}}}}, agentsruntime.WithRunID("unqualified-next"))
	require.NoError(t, err)
	require.Equal(t, "qualified default complete", messageText(out.Final))
	head, err := fx.rt.SessionStore.(session.ContinuationStore).LoadContinuationHead(t.Context(), "default-session")
	require.NoError(t, err)
	require.NotNil(t, head)
	assert.Equal(t, first, head.ID, "unqualified run cannot replace a good continuation")
}

func recordDefaultWorkflowHistory(t *testing.T, c client.Client, resident bool) {
	t.Helper()
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	eng := newTemporalAcceptanceEngine(t, c)
	options := append(defaultHistoryOptions(), agentsruntime.WithEngine(eng))
	fx := newFeatureRuntime(t, options...)
	runID := runDefaultHistoryScenario(t, ctx, fx, resident)
	filename := workflowDefaultHistoryFile
	if resident {
		filename = workflowNonTextResidentHistoryFile
	}
	recordCompletedWorkflowHistory(t, ctx, c, eng, runID, filename)
}

func recordSelvedgeDefaultHistory(t *testing.T, resident bool) {
	t.Helper()
	ctx, cancel := selvedgeScenarioContext(t)
	defer cancel()
	eng, observed := newSelvedgeRecordingEngine(t)
	options := append(defaultHistoryOptions(), agentsruntime.WithEngine(eng))
	fx := newFeatureRuntime(t, options...)
	runID := runDefaultHistoryScenario(t, ctx, fx, resident)
	filename := workflowDefaultHistoryFile
	if resident {
		filename = workflowNonTextResidentHistoryFile
	}
	recordCompletedSelvedgeHistory(t, ctx, eng, observed, runID, filename)
}

func TestGeneratedDefaultWorkflowReplay(t *testing.T) {
	for _, filename := range []string{workflowDefaultHistoryFile, workflowNonTextResidentHistoryFile} {
		t.Run(filepath.Base(filename), func(t *testing.T) {
			data, err := os.ReadFile(filename) // #nosec G304 -- filename comes only from the two constant replay fixture paths.
			require.NoError(t, err)
			history, err := client.HistoryFromJSON(bytes.NewReader(data), client.HistoryJSONOptions{})
			require.NoError(t, err)
			eng, err := temporalengine.NewWorker(temporalengine.Options{ClientOptions: &client.Options{DataConverter: temporalengine.NewAgentDataConverter(nil)}, WorkerOptions: temporalengine.WorkerOptions{TaskQueue: "replay"}, Instrumentation: temporalengine.InstrumentationOptions{DisableTracing: true, DisableMetrics: true}})
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, eng.Close())
			})
			// No live self-certification; the recorded qualification alone enables replay.
			observed := &replayCommandObservations{}
			fx := newFeatureRuntime(t, agentsruntime.WithEngine(observingSelvedgeEngine{Engine: eng, observed: observed}), agentsruntime.WithoutContextDefault(coordinator.AgentID))
			registerDefaultHistoryAgent(t, fx)
			calls := forbidDefaultReplayEffects(t, fx)
			require.NoError(t, eng.ReplayWorkflowHistory(silentTemporalLogger{}, history))
			assert.Zero(t, calls.Load())
			assertDefaultReplayEvidence(t, observed, filename == workflowNonTextResidentHistoryFile)
			fx.recorder.mu.Lock()
			assert.Empty(t, fx.recorder.events, "passive replay must emit no hooks")
			fx.recorder.mu.Unlock()
		})
	}
}

type qualifiedMultimodalClient struct{ multimodalHistoryClient }

func (qualifiedMultimodalClient) ProviderConfiguration(context.Context, *model.Request) string {
	return "fixture-qualified"
}

func TestGeneratedDefaultSupportingAttachmentEvictedAndRecalled(t *testing.T) {
	fx := newFeatureRuntime(t, defaultHistoryOptions()...)
	require.NoError(t, fx.rt.RegisterModel("history", qualifiedMultimodalClient{multimodalHistoryClient{modelClientFunc: func(context.Context, *model.Request) (*model.Response, error) {
		return &model.Response{Content: []model.Message{{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "measured"}}}}, StopReason: "end_turn"}, nil
	}}}))
	require.NoError(t, coordinator.RegisterUsedToolsets(t.Context(), fx.rt, coordinator.WithWorkflowExecutor(newRecordingWorkflowExecutor())))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{Planner: multimodalHistoryPlanner{}}))
	_, err := fx.rt.CreateSession(t.Context(), "supporting-session")
	require.NoError(t, err)
	seed := []*model.Message{
		{Role: "user", Parts: []model.Part{model.TextPart{Text: "remember image"}, model.ImagePart{Format: "png", Bytes: []byte{1, 2, 3}}}},
		{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "noted"}}},
		{Role: "user", Parts: []model.Part{model.TextPart{Text: "remember PDF"}, model.DocumentPart{Name: "manual", Format: "pdf", Bytes: []byte("%PDF-test")}}},
		{Role: "assistant", Parts: []model.Part{model.TextPart{Text: strings.Repeat("history ", 12000)}}},
		{Role: "user", Parts: []model.Part{model.TextPart{Text: "recall both"}}},
	}
	out, err := coordinator.NewClient(fx.rt).Run(t.Context(), "supporting-session", seed, agentsruntime.WithRunID("supporting-run"), agentsruntime.WithContextOverrides(agentsruntime.ContextOverrides{KeepRecentTokens: new(100), ProtectRecentToolTokens: new(3300), MinSavingTokens: new(1)}))
	require.NoError(t, err)
	assert.Equal(t, "recalled image and PDF", messageText(out.Final))
	assert.Positive(t, fx.recorder.count(hooks.ContextCompacted))
	assert.Positive(t, fx.recorder.count(hooks.ContextRecalled))
	record, err := runrecord.Read(t.Context(), fx.rt.RunStore, fx.rt.RunEventStore, out.RunID, out.RunID)
	require.NoError(t, err)
	assert.True(t, record.Runs[0].ContextSettings.Default)
	assert.False(t, record.Runs[0].ContextSettings.Threshold)
}
