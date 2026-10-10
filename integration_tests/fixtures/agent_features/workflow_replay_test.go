package agentfeatures_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"testing"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"example.com/agentfeatures/gen/features/toolsets/workflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	temporalengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protopath"
	"google.golang.org/protobuf/reflect/protorange"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type historyReplayPlanner struct{}

const (
	workflowHistoryFile        = "testdata/workflow_protocol_v3.json"
	workflowHistoryIdentity    = "loom-mcp-history-recorder"
	workflowHistoryStickyQueue = "loom-mcp-history-recorder-sticky"
)

func TestGeneratedFeatureWorkflowReplay(t *testing.T) {
	for _, test := range []struct {
		name    string
		rewrite bool
		missing bool
	}{
		{name: "current"},
		{name: "future", rewrite: true},
		{name: "missing", rewrite: true, missing: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := os.ReadFile(workflowHistoryFile)
			require.NoError(t, err)
			history, err := client.HistoryFromJSON(bytes.NewReader(data), client.HistoryJSONOptions{})
			require.NoError(t, err)
			assertWorkflowHistoryAnonymized(t, history)
			assertHistoryToolContent(t, history)
			started := history.Events[0].GetWorkflowExecutionStartedEventAttributes()
			require.NotNil(t, started)
			require.Len(t, started.Input.Payloads, 1)
			payload := started.Input.Payloads[0]
			var input map[string]jsontext.Value
			require.NoError(t, json.Unmarshal(payload.Data, &input))
			var recordedVersion int
			require.NoError(t, json.Unmarshal(input["WorkflowProtocolVersion"], &recordedVersion))
			assert.Equal(t, api.WorkflowProtocolVersion, recordedVersion, "re-record history after a workflow protocol bump")
			if test.rewrite {
				if test.missing {
					delete(input, "WorkflowProtocolVersion")
				} else {
					input["WorkflowProtocolVersion"], err = json.Marshal(api.WorkflowProtocolVersion + 1)
					require.NoError(t, err)
				}
				payload.Data, err = json.Marshal(input)
				require.NoError(t, err)
			}
			eng, err := temporalengine.NewWorker(temporalengine.Options{
				ClientOptions:   &client.Options{DataConverter: temporalengine.NewAgentDataConverter(nil)},
				WorkerOptions:   temporalengine.WorkerOptions{TaskQueue: "replay"},
				Instrumentation: temporalengine.InstrumentationOptions{DisableTracing: true, DisableMetrics: true},
			})
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, eng.Close())
			})
			fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
			registerHistoryReplayAgent(t, fx)
			err = eng.ReplayWorkflowHistory(silentTemporalLogger{}, history)
			if test.rewrite {
				assert.ErrorContains(t, err, "workflow protocol mismatch")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// recordWorkflowHistory records a real completed execution, never a synthetic history.
func recordWorkflowHistory(t *testing.T, temporalClient client.Client) {
	t.Helper()
	ctx, cancel := temporalScenarioContext(t)
	defer cancel()
	eng := newTemporalAcceptanceEngine(t, temporalClient)
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng))
	registerHistoryReplayAgent(t, fx)
	_, err := fx.rt.CreateSession(ctx, "sess-protocol-history")
	require.NoError(t, err)
	out, err := coordinator.NewClient(fx.rt).Run(ctx, "sess-protocol-history", nil, agentsruntime.WithRunID("run-protocol-history"))
	require.NoError(t, err)
	assert.Equal(t, "history replay complete", messageText(out.Final))
	history := &historypb.History{}
	events := temporalClient.GetWorkflowHistory(ctx, out.RunID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for events.HasNext() {
		event, err := events.Next()
		require.NoError(t, err)
		history.Events = append(history.Events, event)
	}
	require.NotEmpty(t, history.Events)
	require.Equal(t, enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED, history.Events[len(history.Events)-1].EventType)
	require.NoError(t, scrubWorkflowHistory(history))
	require.NoError(t, eng.ReplayWorkflowHistory(silentTemporalLogger{}, history))
	data, err := (protojson.MarshalOptions{Indent: "  "}).Marshal(history)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll("testdata", 0o755))
	require.NoError(t, os.WriteFile(workflowHistoryFile, append(data, '\n'), 0o644))
}

// scrubWorkflowHistory removes recording-machine identities and sticky queue
// names from every reachable protobuf message, including new event attributes.
func scrubWorkflowHistory(history *historypb.History) error {
	return protorange.Range(history.ProtoReflect(), func(values protopath.Values) error {
		message, ok := values.Index(-1).Value.Interface().(protoreflect.Message)
		if !ok {
			return nil
		}
		if identity := message.Descriptor().Fields().ByName("identity"); identity != nil {
			message.Set(identity, protoreflect.ValueOfString(workflowHistoryIdentity))
		}
		if queue, ok := message.Interface().(*taskqueuepb.TaskQueue); ok && queue.GetKind() == enumspb.TASK_QUEUE_KIND_STICKY {
			queue.Name = workflowHistoryStickyQueue
		}
		return nil
	})
}

// assertWorkflowHistoryAnonymized checks all recorded identities and sticky queues.
func assertWorkflowHistoryAnonymized(t *testing.T, history *historypb.History) {
	t.Helper()
	identities, stickyQueues := 0, 0
	err := protorange.Range(history.ProtoReflect(), func(values protopath.Values) error {
		message, ok := values.Index(-1).Value.Interface().(protoreflect.Message)
		if !ok {
			return nil
		}
		if identity := message.Descriptor().Fields().ByName("identity"); identity != nil {
			identities++
			assert.Equal(t, workflowHistoryIdentity, message.Get(identity).String(), "identity at %s", values.Path)
		}
		if queue, ok := message.Interface().(*taskqueuepb.TaskQueue); ok && queue.GetKind() == enumspb.TASK_QUEUE_KIND_STICKY {
			stickyQueues++
			assert.Equal(t, workflowHistoryStickyQueue, queue.GetName(), "sticky queue at %s", values.Path)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Positive(t, identities)
	assert.Positive(t, stickyQueues)
}

func registerHistoryReplayAgent(t *testing.T, fx *featureRuntime) {
	t.Helper()
	registerCoordinatorToolsets(t, t.Context(), fx.rt, newRecordingWorkflowExecutor())
	require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{Planner: historyReplayPlanner{}}))
}

func (historyReplayPlanner) PlanStart(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{ToolCalls: []planner.ToolRequest{{
		Name: workflow.Draft, ToolCallID: "history-draft", Payload: rawjson.Message(`{"topic":"history replay"}`),
	}}}, nil
}

func (historyReplayPlanner) PlanResume(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
		Role:  model.ConversationRoleAssistant,
		Parts: []model.Part{model.TextPart{Text: "history replay complete"}},
	}}}, nil
}

// assertHistoryToolContent requires stored tool content and one K5 finish activity.
func assertHistoryToolContent(t *testing.T, history *historypb.History) {
	t.Helper()
	var toolSchedule int64
	var found bool
	finishCalls := 0
	for _, event := range history.Events {
		if scheduled := event.GetActivityTaskScheduledEventAttributes(); scheduled != nil && scheduled.ActivityType.Name == "features.coordinator.executetool" {
			toolSchedule = event.EventId
			var input api.ToolInput
			require.NoError(t, json.Unmarshal(scheduled.Input.Payloads[0].Data, &input))
			assert.Equal(t, "run-protocol-history", input.RootRunID)
		}
		if scheduled := event.GetActivityTaskScheduledEventAttributes(); scheduled != nil {
			switch scheduled.ActivityType.Name {
			case agentsruntime.FinishRunActivityName(coordinator.AgentID):
				finishCalls++
				var input api.FinishRunInput
				require.NoError(t, json.Unmarshal(scheduled.Input.Payloads[0].Data, &input))
				assert.Equal(t, "run-protocol-history", input.RootRunID)
				require.Len(t, input.ToolEvents, 1)
				assert.NotEmpty(t, input.ToolEvents[0].Refs.Result)
				assert.Empty(t, input.ToolEvents[0].Result)
			case "features.coordinator.resume":
				var input api.PlanActivityInput
				require.NoError(t, json.Unmarshal(scheduled.Input.Payloads[0].Data, &input))
				require.Len(t, input.ToolOutputs, 1)
				assert.NotEmpty(t, input.ToolOutputs[0].Refs.Result)
				assert.Empty(t, input.ToolOutputs[0].Result)
			}
		}
		if completed := event.GetWorkflowExecutionCompletedEventAttributes(); completed != nil {
			var output api.RunOutput
			require.NoError(t, json.Unmarshal(completed.Result.Payloads[0].Data, &output))
			assert.NotEmpty(t, output.ToolEventsRef)
			assert.Equal(t, 1, output.ToolEventCount)
		}

		if completed := event.GetActivityTaskCompletedEventAttributes(); completed != nil && toolSchedule != 0 && completed.ScheduledEventId == toolSchedule {
			var output api.ToolOutput
			require.NoError(t, json.Unmarshal(completed.Result.Payloads[0].Data, &output))
			assert.NotEmpty(t, output.Refs.Result)
			assert.NotEmpty(t, output.Refs.Payload)
			assert.NotEmpty(t, output.Payload)
			found = true
		}
	}
	assert.True(t, found, "recorded history must contain a completed tool activity with stored content")
	assert.Equal(t, 1, finishCalls)
}
