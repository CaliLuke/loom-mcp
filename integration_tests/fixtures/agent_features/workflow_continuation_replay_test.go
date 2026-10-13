package agentfeatures_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	temporalengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
)

type continuationHistoryPlanner struct{}

const workflowContinuationHistoryFile = "testdata/workflow_protocol_v19_continued.json"

func TestGeneratedContinuationWorkflowReplay(t *testing.T) {
	data, err := os.ReadFile(workflowContinuationHistoryFile)
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
	registerContinuationHistoryAgent(t, fx)
	require.NoError(t, eng.ReplayWorkflowHistory(silentTemporalLogger{}, history))
	// Recorded start output must prove actual seeding, not an edited protocol stamp.
	seeded := false
	for _, event := range history.Events {
		done := event.GetActivityTaskCompletedEventAttributes()
		if done == nil || done.Result == nil {
			continue
		}
		for _, payload := range done.Result.Payloads {
			var out api.RecordContentOutput
			if err := temporalengine.NewAgentDataConverter(nil).FromPayload(payload, &out); err == nil && out.Continuation != nil {
				assert.Equal(t, "continued", out.Continuation.Outcome)
				assert.Equal(t, 2, out.Continuation.Pos)
				seeded = true
			}
		}
	}
	assert.True(t, seeded)
}
func (continuationHistoryPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	if err := measureHistoryRequest(ctx, in.Agent, in.Messages); err != nil {
		return nil, err
	}
	if len(in.Messages) > 1 && messageText(in.Messages[len(in.Messages)-2]) != "carry this decision" {
		return nil, fmt.Errorf("continued decision missing")
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "carry this decision"}}}}}, nil
}

func (continuationHistoryPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return nil, fmt.Errorf("unexpected resume")
}

func registerContinuationHistoryAgent(t *testing.T, fx *featureRuntime) {
	t.Helper()
	require.NoError(t, fx.rt.RegisterModel("history", modelClientFunc(func(context.Context, *model.Request) (*model.Response, error) {
		return &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "measured"}}}}, StopReason: "end_turn"}, nil
	})))
	require.NoError(t, coordinator.RegisterUsedToolsets(t.Context(), fx.rt, coordinator.WithWorkflowExecutor(newRecordingWorkflowExecutor())))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{Planner: continuationHistoryPlanner{}}))
}

func recordContinuationWorkflowHistory(t *testing.T, c client.Client) {
	t.Helper()
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	eng := newTemporalAcceptanceEngine(t, c)
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
	recordCompletedWorkflowHistory(t, ctx, c, eng, second.RunID, workflowContinuationHistoryFile)
}
