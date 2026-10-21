package agentfeatures_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	temporalengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
)

type multimodalHistoryPlanner struct{}
type multimodalHistoryClient struct{ modelClientFunc }

const workflowMultimodalHistoryFile = "testdata/workflow_protocol_v21_multimodal.json"

func TestGeneratedMultimodalWorkflowReplay(t *testing.T) {
	assert.Equal(t, 21, api.WorkflowProtocolVersion)
	data, err := os.ReadFile(workflowMultimodalHistoryFile)
	require.NoError(t, err)
	history, err := client.HistoryFromJSON(bytes.NewReader(data), client.HistoryJSONOptions{})
	require.NoError(t, err)
	assertWorkflowHistoryAnonymized(t, history)
	scheduled := map[int64]bool{}
	for _, event := range history.Events {
		if input := event.GetActivityTaskScheduledEventAttributes(); input != nil && input.ActivityType.Name == agentsruntime.RecallActivityName(coordinator.AgentID) {
			var recallInput api.RecallActivityInput
			require.NoError(t, temporalengine.NewAgentDataConverter(nil).FromPayload(input.Input.Payloads[0], &recallInput))
			assert.Positive(t, recallInput.PartTokens)
			assert.Contains(t, recallInput.Parts.ImageFormats, model.ImageFormatPNG)
			assert.Contains(t, recallInput.Parts.DocumentFormats, model.DocumentFormatPDF)
			scheduled[event.EventId] = true
		}
		if done := event.GetActivityTaskCompletedEventAttributes(); done != nil && scheduled[done.ScheduledEventId] {
			var output api.RecallActivityOutput
			require.NoError(t, temporalengine.NewAgentDataConverter(nil).FromPayload(done.Result.Payloads[0], &output))
			assert.NotEmpty(t, output.Parts)
			assert.Equal(t, output.Parts, output.Result.Refs.Parts)
			assert.Positive(t, output.NonText)
			assert.Empty(t, output.Code)
		}
	}
	assert.Len(t, scheduled, 2)

	eng, err := temporalengine.NewWorker(temporalengine.Options{ClientOptions: &client.Options{DataConverter: temporalengine.NewAgentDataConverter(nil)}, WorkerOptions: temporalengine.WorkerOptions{TaskQueue: "replay"}, Instrumentation: temporalengine.InstrumentationOptions{DisableTracing: true, DisableMetrics: true}})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, eng.Close())
	})
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
	registerMultimodalHistoryAgent(t, fx)
	require.NoError(t, eng.ReplayWorkflowHistory(silentTemporalLogger{}, history))
}

func (multimodalHistoryClient) ToolResultParts(context.Context, *model.Request) model.ToolResultPartSupport {
	return model.ToolResultPartSupport{ImageFormats: []model.ImageFormat{model.ImageFormatPNG}, DocumentFormats: []model.DocumentFormat{model.DocumentFormatPDF}, MaxImageBytes: 10000, MaxDocumentBytes: 10000}
}
func (multimodalHistoryPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	if err := measureHistoryRequest(ctx, in.Agent, in.Messages); err != nil {
		return nil, err
	}
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "workflow.draft", ToolCallID: "initial", Payload: rawjson.Message(`{"topic":"start"}`)}}}, nil
}
func (multimodalHistoryPlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	if err := measureHistoryRequest(ctx, in.Agent, in.Messages); err != nil {
		return nil, err
	}
	count := 0
	for _, message := range in.Messages {
		for _, part := range message.Parts {
			if result, ok := part.(model.ToolResultPart); ok {
				count += len(result.Parts)
			}
		}
	}
	if count == 2 {
		return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "recalled image and PDF"}}}}}, nil
	}
	for _, out := range in.ToolOutputs {
		if out.ToolCallID == "image" {
			return nil, fmt.Errorf("attachments missing from recalled view")
		}
	}
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{
		{Name: tools.RecallContext, ToolCallID: "image", Payload: rawjson.Message(`{"read":{"id":"1","part":"attachment","index":2,"offset":0}}`)},
		{Name: tools.RecallContext, ToolCallID: "pdf", Payload: rawjson.Message(`{"read":{"id":"3","part":"attachment","index":2,"offset":0}}`)},
	}}, nil
}
func registerMultimodalHistoryAgent(t *testing.T, fx *featureRuntime) {
	t.Helper()
	require.NoError(t, fx.rt.RegisterModel("history", multimodalHistoryClient{modelClientFunc: func(context.Context, *model.Request) (*model.Response, error) {
		return &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "measured"}}}}, StopReason: "end_turn"}, nil
	}}))
	require.NoError(t, coordinator.RegisterUsedToolsets(t.Context(), fx.rt, coordinator.WithWorkflowExecutor(newRecordingWorkflowExecutor())))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{Planner: multimodalHistoryPlanner{}}))
}
func recordMultimodalWorkflowHistory(t *testing.T, c client.Client) {
	t.Helper()
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	eng := newTemporalAcceptanceEngine(t, c)
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
	recordCompletedWorkflowHistory(t, ctx, c, eng, out.RunID, workflowMultimodalHistoryFile)
}
