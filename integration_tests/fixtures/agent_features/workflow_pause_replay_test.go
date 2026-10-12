package agentfeatures_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"testing"
	"time"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"example.com/agentfeatures/gen/features/toolsets/workflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
)

type pauseHistoryPlanner struct{}

type pauseHistoryExecutor struct{ recordingWorkflowExecutor }

const workflowPauseHistoryFile = "testdata/workflow_protocol_v18_confirmed_pause.json"

// recordPauseWorkflowHistory answers a pause returned only after tool approval.
func recordPauseWorkflowHistory(t *testing.T, c client.Client) {
	t.Helper()
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	eng := newTemporalAcceptanceEngine(t, c)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng), historyConfirmation())
	registerPauseHistoryAgent(t, fx)
	t.Cleanup(func() {
		if t.Failed() {
			logTemporalAwaitFailure(t, c, fx, "run-pause-history")
		}
	})
	_, err := fx.rt.CreateSession(ctx, "sess-pause-history")
	require.NoError(t, err)
	handle, err := coordinator.NewClient(fx.rt).Start(ctx, "sess-pause-history", []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "draft then ask"}}}}, agentsruntime.WithRunID("run-pause-history"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return fx.recorder.count(hooks.AwaitConfirmation) == 1
	}, 5*time.Second, 10*time.Millisecond)
	assert.Zero(t, fx.recorder.count(hooks.AwaitClarification))
	require.NoError(t, fx.rt.ProvideConfirmation(ctx, &api.ConfirmationDecision{RunID: "run-pause-history", Approved: true, RequestedBy: "history-recorder"}))
	require.Eventually(t, func() bool {
		return fx.recorder.count(hooks.AwaitClarification) == 1
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, fx.rt.ProvideClarification(ctx, &api.ClarificationAnswer{RunID: "run-pause-history", ID: "confirmed-pause", Answer: workflowHistoryAnswer}))
	out, err := handle.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, "pause replay complete", messageText(out.Final))
	history := recordCompletedWorkflowHistory(t, ctx, c, eng, out.RunID, workflowPauseHistoryFile)
	assertHistoryConfirmedPause(t, history)
}

func registerPauseHistoryAgent(t *testing.T, fx *featureRuntime) {
	t.Helper()
	require.NoError(t, coordinator.RegisterUsedToolsets(t.Context(), fx.rt, coordinator.WithWorkflowExecutor(&pauseHistoryExecutor{})))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{Planner: pauseHistoryPlanner{}}))
}

// assertHistoryConfirmedPause checks command order and the answered resume view.
func assertHistoryConfirmedPause(t *testing.T, history *historypb.History) {
	t.Helper()
	question, err := json.Marshal(&model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: workflowHistoryQuestion}}})
	require.NoError(t, err)
	var approval, prompt, answer, record, recorded, tool, toolCompleted int64
	questionRecord := 0
	tools, resumes, prompts, signals := 0, 0, 0, 0
	for _, event := range history.Events {
		if signal := event.GetWorkflowExecutionSignaledEventAttributes(); signal != nil {
			switch signal.SignalName {
			case api.SignalProvideConfirmation:
				var decision api.ConfirmationDecision
				require.NoError(t, json.Unmarshal(signal.Input.Payloads[0].Data, &decision))
				assert.True(t, decision.Approved)
				approval = event.EventId
			case api.SignalProvideClarification:
				assert.Positive(t, prompt)
				assert.Less(t, prompt, event.EventId)
				var input api.ClarificationAnswer
				require.NoError(t, json.Unmarshal(signal.Input.Payloads[0].Data, &input))
				assert.Equal(t, "confirmed-pause", input.ID)
				assert.NotEmpty(t, input.AnswerRef)
				answer = event.EventId
				signals++
			}
		}
		if completed := event.GetActivityTaskCompletedEventAttributes(); completed != nil {
			if record != 0 && completed.ScheduledEventId == record {
				recorded = event.EventId
			}
			if tool != 0 && completed.ScheduledEventId == tool {
				var output api.ToolOutput
				require.NoError(t, json.Unmarshal(completed.Result.Payloads[0].Data, &output))
				require.NotNil(t, output.Pause)
				require.NotNil(t, output.Pause.Clarification)
				assert.Equal(t, "confirmed-pause", output.Pause.Clarification.ID)
				toolCompleted = event.EventId
			}
		}
		scheduled := event.GetActivityTaskScheduledEventAttributes()
		if scheduled == nil {
			continue
		}
		data := scheduled.Input.Payloads[0].Data
		switch scheduled.ActivityType.Name {
		case "features.coordinator.executetool":
			assert.Positive(t, approval)
			assert.Less(t, approval, event.EventId)
			tools++
			tool = event.EventId
		case "runtime.publish_hook":
			for _, hook := range historyHookInputs(t, data) {
				if hook.Type == hooks.AwaitClarification {
					assert.Equal(t, 1, tools)
					assert.Positive(t, toolCompleted)
					assert.Less(t, toolCompleted, event.EventId)
					var input hooks.AwaitClarificationEvent
					require.NoError(t, json.Unmarshal(hook.Payload, &input))
					assert.Equal(t, "confirmed-pause", input.ID)
					assert.Equal(t, workflowHistoryQuestion, input.Question)
					prompt = event.EventId
					prompts++
				}
			}
		case agentsruntime.RecordContentActivityName(coordinator.AgentID):
			var input api.RecordContentInput
			require.NoError(t, json.Unmarshal(data, &input))
			if !input.SeedMessages && len(input.Items) == 1 && bytes.Equal(input.Items[0], question) {
				assert.Positive(t, answer)
				assert.Less(t, answer, event.EventId)
				record = event.EventId
				questionRecord++
			}
		case "features.coordinator.resume":
			assert.Positive(t, recorded)
			assert.Less(t, recorded, event.EventId)
			var input api.PlanActivityInput
			require.NoError(t, json.Unmarshal(data, &input))
			require.NotEmpty(t, input.View.Pending)
			last := input.View.Pending[len(input.View.Pending)-1]
			assert.Equal(t, index.Clarification, last.Kind)
			assert.Equal(t, len(workflowHistoryAnswer), last.Bytes)
			assert.NotEmpty(t, last.Hash)
			resumes++
		}
	}
	assert.Equal(t, 1, tools)
	assert.Equal(t, 1, prompts)
	assert.Equal(t, 1, signals)
	assert.Equal(t, 1, questionRecord)
	assert.Equal(t, 1, resumes)
}

func (pauseHistoryPlanner) PlanStart(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: workflow.Draft, ToolCallID: "pause-draft", Payload: rawjson.Message(`{"topic":"pause history"}`)}}}, nil
}

func (pauseHistoryPlanner) PlanResume(_ context.Context, in *planner.PlanResumeInput) (*planner.PlanResult, error) {
	if err := validateHistoryClarification(in.Messages); err != nil {
		return nil, err
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "pause replay complete"}}}}}, nil
}

func (e *pauseHistoryExecutor) Execute(ctx context.Context, meta *agentsruntime.ToolCallMeta, call *planner.ToolRequest) (*agentsruntime.ToolExecutionResult, error) {
	result, err := e.recordingWorkflowExecutor.Execute(ctx, meta, call)
	if err != nil {
		return nil, err
	}
	result.Pause = &agentsruntime.ToolPause{Clarification: &agentsruntime.ToolPauseClarification{ID: "confirmed-pause", Question: workflowHistoryQuestion}}
	return result, nil
}
