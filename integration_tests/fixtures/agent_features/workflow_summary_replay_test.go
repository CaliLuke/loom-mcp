package agentfeatures_test

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"time"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
)

type summaryHistoryPlanner struct{}

const workflowSummaryHistoryFile = "testdata/workflow_protocol_v16_summary.json"
const historyClarificationText = "I need a detail before continuing."

const historySummaryText = "# Goal\nRemember the task [message 1].\n# Decisions\nUse evidence.\n# Facts\nHistory is recorded.\n# Progress\nRead it.\n# Open questions\nNone.\n# Next\nFinish."

func recordSummaryWorkflowHistory(t *testing.T, c client.Client) {
	t.Helper()
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	eng := newTemporalAcceptanceEngine(t, c)
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
	require.Eventually(t, func() bool { return fx.recorder.count(hooks.AwaitClarification) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, fx.rt.ProvideClarification(ctx, &api.ClarificationAnswer{RunID: "run-summary-history", ID: "summary-question", Answer: "yes"}))
	out, err := handle.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, "summary replay complete", messageText(out.Final))
	history := recordCompletedWorkflowHistory(t, ctx, c, eng, out.RunID, workflowSummaryHistoryFile)
	assertHistorySummary(t, history)
}

func registerSummaryHistoryAgent(t *testing.T, fx *featureRuntime) {
	t.Helper()
	require.NoError(t, fx.rt.RegisterModel("history", modelClientFunc(func(context.Context, *model.Request) (*model.Response, error) {
		return &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: historyClarificationText}}}}, StopReason: "end_turn"}, nil
	})))
	require.NoError(t, fx.rt.RegisterModel("summary", modelClientFunc(func(context.Context, *model.Request) (*model.Response, error) {
		return &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: historySummaryText}}}}, StopReason: "end_turn"}, nil
	})))
	require.NoError(t, coordinator.RegisterUsedToolsets(t.Context(), fx.rt, coordinator.WithWorkflowExecutor(newRecordingWorkflowExecutor())))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{Planner: summaryHistoryPlanner{}}))
}

func (summaryHistoryPlanner) PlanStart(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
	if err := measureHistoryRequest(ctx, in.Agent, in.Messages); err != nil {
		return nil, err
	}
	return &planner.PlanResult{Await: planner.NewAwait(planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "summary-question", Question: "Ready?"}))}, nil
}
func (summaryHistoryPlanner) PlanResume(ctx context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	if len(in.Messages) < 2 || messageText(in.Messages[len(in.Messages)-2]) != historyClarificationText+"Ready?" || messageText(in.Messages[len(in.Messages)-1]) != "yes" {
		return nil, fmt.Errorf("clarification-only resume must retain model text, question and answer")
	}
	if err := measureHistoryRequest(ctx, in.Agent, in.Messages); err != nil {
		return nil, err
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "summary replay complete"}}}}}, nil
}

func assertHistorySummary(t *testing.T, history *historypb.History) {
	t.Helper()
	summaries, adoptions, resumes, clarifications := 0, 0, 0, 0
	var clarificationActivity int64
	var merged runstore.Hash
	expected, err := json.Marshal([]*model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: historyClarificationText}, model.TextPart{Text: "Ready?"}}}}, json.Deterministic(true))
	require.NoError(t, err)
	expectedHash := runstore.Hash(fmt.Sprintf("sha256:%x", sha256.Sum256(expected)))
	for _, event := range history.Events {
		if completed := event.GetActivityTaskCompletedEventAttributes(); completed != nil && clarificationActivity != 0 && completed.ScheduledEventId == clarificationActivity {
			var out api.RecordContentOutput
			require.NoError(t, json.Unmarshal(completed.Result.Payloads[0].Data, &out))
			require.Len(t, out.Entries, 1)
			assert.Equal(t, index.Assistant, out.Entries[0].Kind)
			merged = out.Entries[0].Hash
			assert.Equal(t, expectedHash, merged, "recorded content must contain original text and question")
		}
		scheduled := event.GetActivityTaskScheduledEventAttributes()
		if scheduled == nil {
			continue
		}
		data := scheduled.Input.Payloads[0].Data
		switch scheduled.ActivityType.Name {
		case agentsruntime.RecordContentActivityName(coordinator.AgentID):
			var in api.RecordContentInput
			require.NoError(t, json.Unmarshal(data, &in))
			if in.Clarification != nil {
				assert.NotEmpty(t, in.Clarification.TranscriptRef)
				assert.Equal(t, "Ready?", in.Clarification.Question)
				assert.Zero(t, resumes)
				clarifications++
				clarificationActivity = event.EventId
			}
		case agentsruntime.SummarizeContextActivityName(coordinator.AgentID):
			summaries++
			var in api.SummarizeContextInput
			require.NoError(t, json.Unmarshal(data, &in))
			assert.Equal(t, "summary", in.ContextModel.ID)
			assert.Equal(t, 2, in.Step)
			assert.Greater(t, in.To, in.From)
		case "runtime.publish_hook":
			for _, hook := range historyHookInputs(t, data) {
				if hook.Type != hooks.ContextCompacted {
					continue
				}
				var adopted hooks.ContextCompactionEvent
				require.NoError(t, json.Unmarshal(hook.Payload, &adopted))
				if adopted.Tier != "summary" {
					continue
				}
				adoptions++
				assert.NotEmpty(t, adopted.CallKeys)
				assert.NotNil(t, adopted.Checkpoint)
				assert.NotNil(t, adopted.Prompt)
				assert.Equal(t, 1, adopted.Chunks)
				assert.Zero(t, resumes)
			}
		case "features.coordinator.resume":
			resumes++
			assert.Equal(t, 1, adoptions, "summary adopted before resume")
		}
	}
	assert.Equal(t, 1, clarifications, "held transcript must enter the clarification recording")
	assert.Equal(t, expectedHash, merged)
	assert.Equal(t, 1, summaries)
	assert.Equal(t, 1, adoptions)
	assert.Equal(t, 1, resumes)
}
