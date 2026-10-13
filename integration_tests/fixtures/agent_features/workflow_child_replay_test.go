package agentfeatures_test

import (
	"bytes"
	jsonopts "encoding/json"
	"encoding/json/v2"
	"os"
	"testing"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"example.com/agentfeatures/gen/features/agents/specialist"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	temporalengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
)

const (
	workflowChildHistoryFile    = "testdata/workflow_protocol_v19_child.json"
	workflowChildRunHistoryFile = "testdata/workflow_protocol_v19_child_run.json"
)

func TestGeneratedFeatureChildWorkflowReplay(t *testing.T) {
	root, err := os.OpenRoot(".")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, root.Close())
	})
	for _, filename := range []string{workflowChildHistoryFile, workflowChildRunHistoryFile} {
		t.Run(filename, func(t *testing.T) {
			data, err := root.ReadFile(filename)
			require.NoError(t, err)
			history, err := client.HistoryFromJSON(bytes.NewReader(data), client.HistoryJSONOptions{})
			require.NoError(t, err)
			assertWorkflowHistoryAnonymized(t, history)
			var input api.RunInput
			require.NoError(t, json.Unmarshal(history.Events[0].GetWorkflowExecutionStartedEventAttributes().Input.Payloads[0].Data, &input, jsonopts.FormatDurationAsNano(true)))
			assert.Equal(t, api.WorkflowProtocolVersion, input.WorkflowProtocolVersion)
			require.NotEmpty(t, input.ContextFingerprint)
			if filename == workflowChildHistoryFile {
				assertHistoryChildStart(t, history, input)
			} else {
				assert.Equal(t, specialist.AgentID, input.AgentID)
				assert.Equal(t, "run-parent-child", input.ParentRunID)
				assert.Zero(t, input.ContextSettings.ContextWindow)
				assert.Nil(t, input.Policy)
			}
			eng, err := temporalengine.NewWorker(temporalengine.Options{ClientOptions: &client.Options{DataConverter: temporalengine.NewAgentDataConverter(nil)}, WorkerOptions: temporalengine.WorkerOptions{TaskQueue: "replay"}, Instrumentation: temporalengine.InstrumentationOptions{DisableTracing: true, DisableMetrics: true}})
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, eng.Close())
			})
			fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
			require.NoError(t, specialist.RegisterSpecialistAgent(t.Context(), fx.rt, specialist.SpecialistAgentConfig{Planner: &specialistFinalResultPlanner{}}))
			registerCoordinatorToolsets(t, t.Context(), fx.rt, newRecordingWorkflowExecutor())
			require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{Planner: parentDelegationPlanner{}}))
			require.NoError(t, eng.ReplayWorkflowHistory(silentTemporalLogger{}, history))
		})
	}
}

func recordChildWorkflowHistory(t *testing.T, temporalClient client.Client) {
	t.Helper()
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	eng := newTemporalAcceptanceEngine(t, temporalClient)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
	runGeneratedChildScenario(t, ctx, fx, agentsruntime.WithContextOverrides(agentsruntime.ContextOverrides{ContextWindow: new(8000)}))
	recordCompletedWorkflowHistory(t, ctx, temporalClient, eng, "run-parent-child", workflowChildHistoryFile)
	child := fx.recorder.childRunID("run-parent-child")
	require.NotEmpty(t, child)
	recordCompletedWorkflowHistory(t, ctx, temporalClient, eng, child, workflowChildRunHistoryFile)
}

// assertHistoryChildStart proves preparation completes before the child command,
// and that its exact independent settings and fingerprint enter the child input.
func assertHistoryChildStart(t *testing.T, history *historypb.History, parent api.RunInput) {
	t.Helper()
	assert.Equal(t, 8000, parent.ContextSettings.ContextWindow)
	var scheduled, completed int64
	var prepared api.ContextStartOutput
	children := 0
	for _, event := range history.Events {
		if task := event.GetActivityTaskScheduledEventAttributes(); task != nil && task.ActivityType.Name == agentsruntime.RecordContentActivityName(specialist.AgentID) {
			var input api.RecordContentInput
			require.NoError(t, json.Unmarshal(task.Input.Payloads[0].Data, &input))
			require.NotNil(t, input.ContextStart)
			assert.Equal(t, specialist.AgentID, input.ContextStart.AgentID)
			assert.Equal(t, "run-parent-child", input.RootRunID)
			assert.Zero(t, input.ContextStart.Settings.ContextWindow)
			scheduled = event.EventId
		}
		if task := event.GetActivityTaskCompletedEventAttributes(); task != nil && scheduled != 0 && task.ScheduledEventId == scheduled {
			var output api.RecordContentOutput
			require.NoError(t, json.Unmarshal(task.Result.Payloads[0].Data, &output))
			require.NotNil(t, output.ContextStart)
			prepared = *output.ContextStart
			completed = event.EventId
		}
		if child := event.GetStartChildWorkflowExecutionInitiatedEventAttributes(); child != nil {
			children++
			require.Positive(t, completed)
			assert.Less(t, completed, event.EventId)
			var input api.RunInput
			require.NoError(t, json.Unmarshal(child.Input.Payloads[0].Data, &input, jsonopts.FormatDurationAsNano(true)))
			assert.Equal(t, prepared.Settings, input.ContextSettings)
			assert.Equal(t, prepared.Fingerprint, input.ContextFingerprint)
			assert.NotEmpty(t, input.ContextSettings.Prompt.Template)
			assert.NotEqual(t, parent.ContextFingerprint, input.ContextFingerprint)
			assert.Equal(t, "features.specialist.context.summary", string(input.ContextSettings.Prompt.Ref.ID))
			assert.Zero(t, input.ContextSettings.ContextWindow)
			assert.Nil(t, input.Policy)
		}
	}
	assert.Equal(t, 1, children)
}
