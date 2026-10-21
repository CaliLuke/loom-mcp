package agentfeatures_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"example.com/agentfeatures/gen/features/agents/specialist"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	temporalengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	sv "github.com/CaliLuke/selvedge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

const workflowModelCallHistoryFile = "testdata/workflow_protocol_v21_model_call_budget.json"

func TestGeneratedModelCallWorkflowReplay(t *testing.T) {
	root, err := os.OpenRoot("testdata")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, root.Close())
	})
	t.Run("temporal", func(t *testing.T) {
		data, err := root.ReadFile(filepath.Base(workflowModelCallHistoryFile))
		require.NoError(t, err)
		history, err := client.HistoryFromJSON(bytes.NewReader(data), client.HistoryJSONOptions{})
		require.NoError(t, err)
		assertWorkflowHistoryAnonymized(t, history)
		last := history.Events[len(history.Events)-1].GetWorkflowExecutionFailedEventAttributes()
		require.NotNil(t, last)
		require.NotNil(t, last.Failure.GetApplicationFailureInfo())
		assert.Equal(t, "loom_mcp.model_call_cap", last.Failure.GetApplicationFailureInfo().Type)
		assert.True(t, last.Failure.GetApplicationFailureInfo().NonRetryable)
		eng, err := temporalengine.NewWorker(temporalengine.Options{ClientOptions: &client.Options{DataConverter: temporalengine.NewAgentDataConverter(nil)}, WorkerOptions: temporalengine.WorkerOptions{TaskQueue: "replay"}, Instrumentation: temporalengine.InstrumentationOptions{DisableTracing: true, DisableMetrics: true}})
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, eng.Close())
		})
		observed := &replayCommandObservations{}
		fx := newFeatureRuntime(t, agentsruntime.WithEngine(observingSelvedgeEngine{Engine: eng, observed: observed}))
		effects := prepareModelCallBudgetReplay(t, fx)
		require.NoError(t, eng.ReplayWorkflowHistory(silentTemporalLogger{}, history))
		assert.Zero(t, effects.Load())
		assertModelCallReplayEvidence(t, observed)
	})
	t.Run("selvedge", func(t *testing.T) {
		data, err := root.ReadFile(filepath.Join("selvedge", filepath.Base(workflowModelCallHistoryFile)))
		require.NoError(t, err)
		history, err := sv.ParseHistory(data)
		require.NoError(t, err)
		assert.Equal(t, sv.StatusFailed, history.Instance.Status)
		eng, live := newSelvedgeRecordingEngine(t)
		observed := &replayCommandObservations{}
		fx := newFeatureRuntime(t, agentsruntime.WithEngine(observingSelvedgeEngine{Engine: eng, observed: observed}))
		effects := prepareModelCallBudgetReplay(t, fx)
		require.NoError(t, eng.ReplayHistory(t.Context(), history))
		assert.Zero(t, effects.Load())
		assert.Zero(t, live.hookCalls())
		assertModelCallReplayEvidence(t, observed)
	})
}

func recordModelCallWorkflowHistory(t *testing.T, c client.Client) {
	t.Helper()
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	eng := newTemporalAcceptanceEngine(t, c)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
	runID := runModelCallHistoryScenario(t, ctx, fx)
	recordTerminalWorkflowHistory(t, ctx, c, eng, runID, workflowModelCallHistoryFile, enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED)
}

func recordSelvedgeModelCallWorkflowHistory(t *testing.T) {
	t.Helper()
	ctx, cancel := selvedgeScenarioContext(t)
	defer cancel()
	eng, live := newSelvedgeRecordingEngine(t)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
	runID := runModelCallHistoryScenario(t, ctx, fx)
	recordTerminalSelvedgeHistory(t, ctx, eng, live, runID, workflowModelCallHistoryFile, sv.StatusFailed)
}

// runModelCallHistoryScenario forces a real activity retry after one reservation.
func runModelCallHistoryScenario(t *testing.T, ctx context.Context, fx *featureRuntime) string {
	t.Helper()
	p := &modelCallBudgetPlanner{calls: 3, retryFirst: true}
	providerCalls := registerModelCallBudgetAgent(t, fx, p)
	_, err := fx.rt.CreateSession(ctx, "model-budget-history-session")
	require.NoError(t, err)
	const runID = "model-budget-history-run"
	out, err := specialist.NewClient(fx.rt).Run(ctx, "model-budget-history-session", []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "record bounded work"}}}}, agentsruntime.WithRunID(runID))
	assert.Equal(t, int32(2), p.attempts.Load())
	assert.Equal(t, int32(3), providerCalls.Load())
	requireModelCallLimit(t, err)
	assert.Nil(t, out)
	record, err := runrecord.Read(ctx, fx.rt.RunStore, fx.rt.RunEventStore, runID, runID)
	require.NoError(t, err)
	require.Len(t, record.Runs[0].Calls, 3)
	require.NotNil(t, record.Runs[0].ModelCallLimit)
	assert.Equal(t, 3, record.Runs[0].ModelCallLimit.Limit)
	assert.Equal(t, 4, record.Runs[0].ModelCallLimit.Call.Seq)
	assert.Equal(t, 2, record.Runs[0].ModelCallLimit.Call.Attempt)
	assert.Equal(t, runrecord.Abandoned, record.Runs[0].Calls[0].Class)
	assert.Equal(t, runrecord.Adopted, record.Runs[0].Calls[1].Class)
	assert.Equal(t, runrecord.Adopted, record.Runs[0].Calls[2].Class)
	require.False(t, t.Failed(), "invalid admission proof must not publish history")
	return runID
}

func prepareModelCallBudgetReplay(t *testing.T, fx *featureRuntime) *atomic.Int64 {
	t.Helper()
	registerModelCallBudgetAgent(t, fx, &modelCallBudgetPlanner{calls: 3, retryFirst: true})
	require.NoError(t, fx.rt.OverridePolicy(specialist.AgentID, agentsruntime.RunPolicy{MaxModelCalls: 9}))
	effects := &atomic.Int64{}
	require.NoError(t, fx.rt.RegisterModel("model-budget", changedReplayModel{calls: effects}))
	fx.rt.RunStore = forbiddenReplayStore{calls: effects}
	fx.rt.RunEventStore = forbiddenReplayLog{calls: effects}
	return effects
}

func assertModelCallReplayEvidence(t *testing.T, observed *replayCommandObservations) {
	t.Helper()
	require.Len(t, observed.modelPolicies, 1)
	assert.Equal(t, specialist.AgentID, observed.modelPolicies[0].AgentID)
	assert.Nil(t, observed.modelPolicies[0].MaxModelCalls, "generated worker default is resolved, not supplied by caller")
	assert.Equal(t, []int{3}, observed.modelCaps, "recorded worker default survives changed live policy")
	require.Len(t, observed.planners, 1)
	assert.Equal(t, 3, observed.planners[0].MaxModelCalls)
	require.Len(t, observed.outputs, 1)
	output := observed.outputs[0]
	require.NotNil(t, output.ModelCallLimit)
	assert.Equal(t, 3, output.ModelCallLimit.Limit)
	assert.Equal(t, 4, output.ModelCallLimit.Call.Seq)
	assert.Equal(t, 2, output.ModelCallLimit.Call.Attempt)
	assert.Nil(t, output.Result, "swallowed refusal cannot yield success")
	require.Len(t, output.CallKeys, 2)
	for i, key := range output.CallKeys {
		assert.Equal(t, i+2, key.Seq)
		assert.Equal(t, 2, key.Attempt)
	}
	assert.Len(t, output.Facts, 2)
}

func assertSelvedgeModelCallObservations(t *testing.T, observations []selvedgeActivityObservation) {
	t.Helper()
	observed := &replayCommandObservations{}
	for _, activity := range observations {
		if activity.Name == agentsruntime.RecordContentActivityName(specialist.AgentID) {
			var input api.RecordContentInput
			require.NoError(t, json.Unmarshal(activity.Input, &input))
			if input.ModelCallPolicy != nil {
				observed.modelPolicies = append(observed.modelPolicies, input.ModelCallPolicy)
				var output api.RecordContentOutput
				require.NoError(t, json.Unmarshal(activity.Output, &output))
				require.NotNil(t, output.MaxModelCalls)
				observed.modelCaps = append(observed.modelCaps, *output.MaxModelCalls)
			}
		}
		if activity.Name == specialist.PlanActivity {
			var input api.PlanActivityInput
			require.NoError(t, json.Unmarshal(activity.Input, &input))
			observed.planners = append(observed.planners, &input)
			var output api.PlanActivityOutput
			require.NoError(t, json.Unmarshal(activity.Output, &output))
			observed.outputs = append(observed.outputs, &output)
		}
	}
	assertModelCallReplayEvidence(t, observed)
}
