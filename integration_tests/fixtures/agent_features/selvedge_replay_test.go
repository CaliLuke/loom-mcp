package agentfeatures_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"example.com/agentfeatures/gen/features/agents/specialist"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	sv "github.com/CaliLuke/selvedge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelvedgeWorkflowReplay(t *testing.T) {
	root, err := os.OpenRoot("testdata/selvedge")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, root.Close())
	})
	for _, suffix := range []string{"", "_child", "_child_run", "_continued", "_multimodal", "_summary", "_confirmed_pause", "_budget"} {
		t.Run("current"+suffix, func(t *testing.T) {
			filename := "workflow_protocol_v18" + suffix + ".json"
			data, err := root.ReadFile(filename)
			require.NoError(t, err)
			history, err := sv.ParseHistory(data)
			require.NoError(t, err)
			require.Equal(t, sv.StatusCompleted, history.Instance.Status)
			start := assertSelvedgeContextStart(t, filename, history)
			eng, live := newSelvedgeRecordingEngine(t)
			observed := &replayCommandObservations{}
			// These deliberately conflict with recorded settings; replay must use the recorded start.
			options := []agentsruntime.RuntimeOption{agentsruntime.WithEngine(observingSelvedgeEngine{Engine: eng, observed: observed})}
			if suffix == "" || suffix == "_confirmed_pause" {
				options = append(options, historyConfirmation())
			}
			if suffix == "" || suffix == "_summary" || suffix == "_budget" {
				options = append(options, agentsruntime.WithContextCompaction(coordinator.AgentID, agentsruntime.ContextCompaction{KeepRecentTokens: 77}), agentsruntime.WithContextLimits(map[string]model.ContextLimits{"history": {ContextWindow: 99999}, "summary": {ContextWindow: 99999}}))
			}
			fx := newFeatureRuntime(t, options...)
			switch suffix {
			case "_child", "_child_run":
				require.NoError(t, specialist.RegisterSpecialistAgent(t.Context(), fx.rt, specialist.SpecialistAgentConfig{Planner: &specialistFinalResultPlanner{}}))
				registerCoordinatorToolsets(t, t.Context(), fx.rt, newRecordingWorkflowExecutor())
				require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{Planner: parentDelegationPlanner{}}))
			case "_continued":
				registerContinuationHistoryAgent(t, fx)
			case "_multimodal":
				registerMultimodalHistoryAgent(t, fx)
			case "_summary":
				registerSummaryHistoryAgent(t, fx)
			case "_confirmed_pause":
				registerPauseHistoryAgent(t, fx)
			case "_budget":
				registerCoordinatorToolsets(t, t.Context(), fx.rt, newRecordingWorkflowExecutor())
				require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{Planner: &externalAwaitPlanner{}}))
			default:
				registerHistoryReplayAgent(t, fx)
			}
			require.NoError(t, eng.ReplayHistory(t.Context(), history))
			assert.Zero(t, live.hookCalls(), "offline replay cannot invoke activities or hook callbacks")
			if suffix != "_child_run" {
				require.NotEmpty(t, observed.planners)
				for _, input := range observed.planners {
					assert.Equal(t, start.ContextFingerprint, input.View.ContextFingerprint)
				}
			}
			for _, input := range observed.summaries {
				assert.Equal(t, start.ContextFingerprint, input.ContextFingerprint)
				assert.Equal(t, start.ContextSettings, input.ContextSettings)
			}
			if suffix == "_summary" {
				assert.NotEmpty(t, observed.summaries)
			}
			if suffix == "_child_run" {
				assert.Equal(t, specialist.AgentID, start.AgentID)
				assert.Equal(t, "run-parent-child", start.ParentRunID)
				assert.Zero(t, start.ContextSettings.ContextWindow)
				assert.Nil(t, start.Policy)
			}
			if suffix == "_child" {
				assertSelvedgeChildStart(t, history, start)
			}
			if suffix == "_continued" {
				assertSelvedgeContinuation(t, history)
			}
			plannerCount, summaryCount := len(observed.planners), len(observed.summaries)
			for _, version := range []int{0, api.WorkflowProtocolVersion + 1} {
				altered, err := sv.ParseHistory(data)
				require.NoError(t, err)
				var input map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(altered.Instance.Input, &input))
				if version == 0 {
					delete(input, "WorkflowProtocolVersion")
				} else {
					input["WorkflowProtocolVersion"], err = json.Marshal(version)
					require.NoError(t, err)
				}
				altered.Instance.Input, err = json.Marshal(input)
				require.NoError(t, err)
				err = eng.ReplayHistory(t.Context(), altered)
				require.ErrorIs(t, err, api.ErrWorkflowProtocolMismatch)
				assert.Contains(t, err.Error(), "workflow protocol mismatch")
				assert.Len(t, observed.planners, plannerCount, "protocol rejected before planner commands")
				assert.Len(t, observed.summaries, summaryCount, "protocol rejected before summary commands")
				assert.Zero(t, live.hookCalls())
			}
		})
	}
	for version := 5; version < 18; version++ {
		t.Run(fmt.Sprintf("protocol%d", version), func(t *testing.T) {
			t.Skip("no Selvedge history exists below protocol18")
		})
	}
}

func assertSelvedgeContinuation(t *testing.T, history *sv.RecordedHistory) {
	t.Helper()
	seeded := false
	for _, event := range history.Events {
		if event.EventType != "ActivityCompleted" {
			continue
		}
		var output api.RecordContentOutput
		if err := json.Unmarshal(event.Data, &output); err == nil && output.Continuation != nil {
			assert.Equal(t, "continued", output.Continuation.Outcome)
			assert.Equal(t, 2, output.Continuation.Pos)
			seeded = true
		}
	}
	assert.True(t, seeded)
}

func assertSelvedgeChildStart(t *testing.T, history *sv.RecordedHistory, parent api.RunInput) {
	t.Helper()
	assert.Equal(t, 8000, parent.ContextSettings.ContextWindow)
	var prepared *api.ContextStartOutput
	var preparationSeq int64
	children := 0
	data, err := os.ReadFile("testdata/selvedge/workflow_protocol_v18_child_run.json")
	require.NoError(t, err)
	childHistory, err := sv.ParseHistory(data)
	require.NoError(t, err)
	var child api.RunInput
	require.NoError(t, json.Unmarshal(childHistory.Instance.Input, &child))
	for _, event := range history.Events {
		if event.Command == "activity:features.specialist.record_content" {
			var output api.RecordContentOutput
			require.NoError(t, json.Unmarshal(event.Data, &output))
			if output.ContextStart != nil {
				prepared = output.ContextStart
				preparationSeq = event.Seq
			}
		}
		if event.Command == "child:features.specialist.workflow" {
			require.NotNil(t, prepared)
			assert.Less(t, preparationSeq, event.Seq)
			assert.Equal(t, prepared.Settings, child.ContextSettings)
			assert.Equal(t, prepared.Fingerprint, child.ContextFingerprint)
			assert.NotEqual(t, parent.ContextFingerprint, child.ContextFingerprint)
			assert.Equal(t, "features.specialist.context.summary", string(child.ContextSettings.Prompt.Ref.ID))
			assert.Zero(t, child.ContextSettings.ContextWindow)
			children++
		}
	}
	assert.Equal(t, 1, children)
}
