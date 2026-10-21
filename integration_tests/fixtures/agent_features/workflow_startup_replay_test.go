package agentfeatures_test

import (
	"bytes"
	"context"
	jsonopts "encoding/json"
	"encoding/json/v2"
	"fmt"
	"os"
	"strings"
	"testing"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	temporalengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
)

type startupHistoryPlanner struct{}

const workflowStartupHistoryFile = "testdata/workflow_protocol_v21_startup.json"

func TestGeneratedStartupWorkflowReplay(t *testing.T) {
	data, err := os.ReadFile(workflowStartupHistoryFile)
	require.NoError(t, err)
	history, err := client.HistoryFromJSON(bytes.NewReader(data), client.HistoryJSONOptions{})
	require.NoError(t, err)
	assertWorkflowHistoryAnonymized(t, history)
	eng, err := temporalengine.NewWorker(temporalengine.Options{ClientOptions: &client.Options{DataConverter: temporalengine.NewAgentDataConverter(nil)}, WorkerOptions: temporalengine.WorkerOptions{TaskQueue: "replay"}, Instrumentation: temporalengine.InstrumentationOptions{DisableTracing: true, DisableMetrics: true}})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, eng.Close())
	})
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
	registerStartupHistoryAgent(t, fx)
	require.NoError(t, eng.ReplayWorkflowHistory(silentTemporalLogger{}, history))
	fx.recorder.mu.Lock()
	assert.Empty(t, fx.recorder.events, "offline replay executes no hook callbacks")
	fx.recorder.mu.Unlock()
	// Prove the actual recorded command stores complete input before its hook.
	stored := false
	published := false
	for _, event := range history.Events {
		scheduled := event.GetActivityTaskScheduledEventAttributes()
		if scheduled == nil {
			continue
		}
		for _, payload := range scheduled.Input.Payloads {
			if scheduled.ActivityType.Name == agentsruntime.RecordContentActivityName(coordinator.AgentID) {
				var input api.RecordContentInput
				require.NoError(t, temporalengine.NewAgentDataConverter(nil).FromPayload(payload, &input))
				if !input.SeedMessages && len(input.Items) == 1 {
					var runInput api.RunInput
					if err := json.Unmarshal(input.Items[0], &runInput, jsonopts.FormatDurationAsNano(true)); err == nil && len(runInput.Messages) == 1 {
						assert.Equal(t, strings.Repeat("x", 1_100_000), messageText(runInput.Messages[0]))
						stored = true
					}
				}
			}
			if scheduled.ActivityType.Name == "runtime.publish_hook" {
				for _, hook := range historyHookInputs(t, payload.Data) {
					if hook.Type == hooks.RunStarted {
						assert.True(t, stored)
						decoded, err := hooks.DecodeFromHookInput(hook)
						require.NoError(t, err)
						started := decoded.(*hooks.RunStartedEvent)
						assert.Nil(t, started.Input)
						assert.NotEmpty(t, started.InputRef)
						assert.LessOrEqual(t, len(hook.Payload), 1_000_000)
						published = true
					}
				}
			}
		}
	}
	assert.True(t, published)
}

func recordStartupWorkflowHistory(t *testing.T, c client.Client) {
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	eng := newTemporalAcceptanceEngine(t, c)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
	runID := runStartupHistoryScenario(t, ctx, fx)
	recordCompletedWorkflowHistory(t, ctx, c, eng, runID, workflowStartupHistoryFile)
}

func recordSelvedgeStartupHistory(t *testing.T) {
	ctx, cancel := selvedgeScenarioContext(t)
	defer cancel()
	eng, observed := newSelvedgeRecordingEngine(t)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
	runID := runStartupHistoryScenario(t, ctx, fx)
	recordCompletedSelvedgeHistory(t, ctx, eng, observed, runID, workflowStartupHistoryFile)
}

func registerStartupHistoryAgent(t *testing.T, fx *featureRuntime) {
	t.Helper()
	require.NoError(t, coordinator.RegisterUsedToolsets(t.Context(), fx.rt, coordinator.WithWorkflowExecutor(newRecordingWorkflowExecutor())))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{Planner: startupHistoryPlanner{}}))
}

func runStartupHistoryScenario(t *testing.T, ctx context.Context, fx *featureRuntime) string {
	t.Helper()
	registerStartupHistoryAgent(t, fx)
	_, err := fx.rt.CreateSession(ctx, "sess-startup-history")
	require.NoError(t, err)
	out, err := coordinator.NewClient(fx.rt).Run(ctx, "sess-startup-history", []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: strings.Repeat("x", 1_100_000)}}}}, agentsruntime.WithRunID("run-startup-history"))
	require.NoError(t, err)
	assert.Equal(t, "startup preserved", messageText(out.Final))
	fx.recorder.mu.Lock()
	defer fx.recorder.mu.Unlock()
	var started *hooks.RunStartedEvent
	for _, event := range fx.recorder.events {
		if event, ok := event.(*hooks.RunStartedEvent); ok {
			started = event
		}
	}
	require.NotNil(t, started)
	assert.Nil(t, started.Input)
	require.NotEmpty(t, started.InputRef)
	data, err := fx.rt.RunStore.Get(ctx, started.RunContext.RootRunID, started.InputRef)
	require.NoError(t, err)
	var input api.RunInput
	require.NoError(t, json.Unmarshal(data, &input, jsonopts.FormatDurationAsNano(true)))
	require.Len(t, input.Messages, 1)
	assert.Equal(t, strings.Repeat("x", 1_100_000), messageText(input.Messages[0]))
	return out.RunID
}

func (startupHistoryPlanner) PlanStart(_ context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	if len(in.Messages) != 1 || messageText(in.Messages[0]) != strings.Repeat("x", 1_100_000) {
		return nil, fmt.Errorf("startup input was not preserved")
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "startup preserved"}}}}}, nil
}

func (startupHistoryPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, fmt.Errorf("unexpected resume")
}
