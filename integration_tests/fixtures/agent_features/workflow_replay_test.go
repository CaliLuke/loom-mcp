package agentfeatures_test

import (
	"bytes"
	"context"
	jsonopts "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"example.com/agentfeatures/gen/features/agents/coordinator"
	"example.com/agentfeatures/gen/features/agents/specialist/exports/delegated"
	"example.com/agentfeatures/gen/features/toolsets/workflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	temporalengine "github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/temporal"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	agentsruntime "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runtime"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
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
	workflowHistoryFile        = "testdata/workflow_protocol_v18.json"
	workflowHistoryIdentity    = "loom-mcp-history-recorder"
	workflowHistoryStickyQueue = "loom-mcp-history-recorder-sticky"
	workflowHistoryQuestion    = "Which topic should the draft use?"
	workflowHistoryAnswer      = "use the reviewed topic"
)

func TestGeneratedFeatureWorkflowReplay(t *testing.T) {
	for _, test := range []struct {
		name     string
		rewrite  bool
		missing  bool
		previous int
		budget   bool
		summary  bool
		pause    bool
	}{
		{name: "current"},
		{name: "summary adoption", summary: true},
		{name: "terminal adoption crosses budget", budget: true},
		{name: "confirmed tool pause", pause: true},
		{name: "protocol 5 rejected before effects", previous: 5},
		{name: "protocol 6 rejected before effects", previous: 6},
		{name: "protocol 7 rejected before effects", previous: 7},
		{name: "protocol 8 rejected before effects", previous: 8},
		{name: "protocol 9 rejected before effects", previous: 9},
		{name: "protocol 10 rejected before effects", previous: 10},
		{name: "protocol 11 rejected before effects", previous: 11},
		{name: "protocol 12 rejected before effects", previous: 12},
		{name: "protocol 13 rejected before effects", previous: 13},
		{name: "protocol 14 rejected before effects", previous: 14},
		{name: "protocol 15 rejected before effects", previous: 15},
		{name: "protocol 16 rejected before effects", previous: 16},
		{name: "protocol 17 rejected before effects", previous: 17},
		{name: "future", rewrite: true},
		{name: "missing", rewrite: true, missing: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			historyFile := workflowHistoryFile
			if test.pause {
				historyFile = workflowPauseHistoryFile
			}
			if test.summary {
				historyFile = workflowSummaryHistoryFile
			}
			if test.budget {
				historyFile = workflowBudgetHistoryFile
			}
			if test.previous != 0 {
				historyFile = fmt.Sprintf("testdata/workflow_protocol_v%d.json", test.previous)
			}
			data, err := os.ReadFile(historyFile)
			require.NoError(t, err)
			history, err := client.HistoryFromJSON(bytes.NewReader(data), client.HistoryJSONOptions{})
			require.NoError(t, err)
			assertWorkflowHistoryAnonymized(t, history)
			started := history.Events[0].GetWorkflowExecutionStartedEventAttributes()
			require.NotNil(t, started)
			require.Len(t, started.Input.Payloads, 1)
			payload := started.Input.Payloads[0]
			var input map[string]jsontext.Value
			require.NoError(t, json.Unmarshal(payload.Data, &input))
			var recordedVersion int
			require.NoError(t, json.Unmarshal(input["WorkflowProtocolVersion"], &recordedVersion))
			switch {
			case test.pause:
				assertHistoryConfirmedPause(t, history)
			case test.summary:
				assertHistorySummary(t, history)
			case test.budget:
				assertHistoryBudgetAdoption(t, history)
			default:
				assertHistoryToolContent(t, history, recordedVersion)
				if recordedVersion >= 11 {
					assertHistoryClarification(t, history)
				}
			}
			if test.previous != 0 {
				assert.Equal(t, test.previous, recordedVersion)
			} else {
				assert.Equal(t, api.WorkflowProtocolVersion, recordedVersion, "re-record history after a workflow protocol bump")
			}
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
			fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng), historyConfirmation())
			switch {
			case test.pause:
				registerPauseHistoryAgent(t, fx)
			case test.summary:
				registerSummaryHistoryAgent(t, fx)
			default:
				registerHistoryReplayAgent(t, fx)
			}
			err = eng.ReplayWorkflowHistory(silentTemporalLogger{}, history)
			if test.rewrite || test.previous != 0 {
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
	fx := newFeatureRuntime(t, agentsruntime.WithEngine(eng), historyConfirmation(), agentsruntime.WithContextCompaction(coordinator.AgentID, agentsruntime.ContextCompaction{}), agentsruntime.WithContextLimits(map[string]model.ContextLimits{"history": {ContextWindow: 6000}}))
	registerHistoryReplayAgent(t, fx)
	_, err := fx.rt.CreateSession(ctx, "sess-protocol-history")
	require.NoError(t, err)
	handle, err := coordinator.NewClient(fx.rt).Start(ctx, "sess-protocol-history", []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "record this request"}}}, {Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "earlier answer"}}}, {Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: strings.Repeat("earlier history ", 2000)}}}, {Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "recent answer"}}}, {Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "continue"}}}}, agentsruntime.WithRunID("run-protocol-history"), agentsruntime.WithContextOverrides(agentsruntime.ContextOverrides{ContextWindow: new(5000), RecallDiscoveryCalls: new(3), RecallReadCalls: new(3)}))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return fx.recorder.count(hooks.AwaitConfirmation) == 1 && fx.recorder.count(hooks.AwaitClarification) == 1
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, fx.rt.ProvideConfirmation(ctx, &api.ConfirmationDecision{RunID: "run-protocol-history", Approved: true, RequestedBy: "history-recorder"}))
	require.NoError(t, fx.rt.ProvideClarification(ctx, &api.ClarificationAnswer{RunID: "run-protocol-history", ID: "history-clarification", Answer: workflowHistoryAnswer}))
	out, err := handle.Wait(ctx)
	require.NoError(t, err)
	assert.Equal(t, "history replay complete", messageText(out.Final))
	recordCompletedWorkflowHistory(t, ctx, temporalClient, eng, out.RunID, workflowHistoryFile)
}

// recordCompletedWorkflowHistory captures, scrubs and replays a real completed run.
func recordCompletedWorkflowHistory(t *testing.T, ctx context.Context, temporalClient client.Client, eng *temporalengine.Engine, runID, filename string) *historypb.History {
	t.Helper()
	history := &historypb.History{}
	events := temporalClient.GetWorkflowHistory(ctx, runID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
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
	require.NoError(t, os.MkdirAll("testdata", 0o750))
	require.NoError(t, os.WriteFile(filename, append(data, '\n'), 0o600))
	return history
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

func historyConfirmation() agentsruntime.RuntimeOption {
	return agentsruntime.WithToolConfirmation(&agentsruntime.ToolConfirmationConfig{Confirm: map[tools.Ident]*agentsruntime.ToolConfirmation{
		workflow.Draft: {
			Prompt: func(context.Context, *planner.ToolRequest) (string, error) {
				return "Draft for history replay?", nil
			},
			DeniedResult: func(context.Context, *planner.ToolRequest) (any, error) {
				return &workflow.DraftResult{OK: false}, nil
			},
		},
	}})
}

func registerHistoryReplayAgent(t *testing.T, fx *featureRuntime) {
	t.Helper()
	require.NoError(t, fx.rt.RegisterModel("history", modelClientFunc(func(_ context.Context, req *model.Request) (*model.Response, error) {
		facts, err := contextmeasure.Measure(req, model.ContextLimits{}, nil)
		if err != nil {
			return nil, err
		}
		if contextmeasure.Estimate(contextmeasure.Rate{}, 0, facts.TextBytes, facts.NonText) > 6000 {
			return nil, model.NewProviderError("history", "complete", 400, model.ProviderErrorKindContextOverflow, "context_length_exceeded", "recorded overflow", "", false, nil)
		}
		return &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "recorded measurement"}}}}, StopReason: "end_turn"}, nil
	})))
	require.NoError(t, coordinator.RegisterUsedToolsets(t.Context(), fx.rt, coordinator.WithWorkflowExecutor(newRecordingWorkflowExecutor())))
	reg, err := coordinator.NewCoordinatorDelegatedAgentToolsetRegistration(fx.rt, "", func(cfg *agentsruntime.AgentToolConfig) {
		cfg.PreChildValidator = func(ctx context.Context, in *agentsruntime.AgentToolValidationInput) *agentsruntime.AgentToolValidationError {
			messages, err := in.View.LoadMessages(ctx)
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(messages), 2)
			assert.Equal(t, true, messages[0].Meta[transcript.CheckpointMetaKey], "child validation must receive the compacted parent checkpoint")
			assert.Contains(t, messageText(messages[0]), "record this request")
			return agentsruntime.NewAgentToolValidationError("topic requires evidence", []*tools.FieldIssue{{Field: "topic", Constraint: "invalid_format"}}, nil)
		}
	})
	require.NoError(t, err)
	require.NoError(t, fx.rt.RegisterToolset(reg))
	require.NoError(t, coordinator.RegisterCoordinatorAgent(t.Context(), fx.rt, coordinator.CoordinatorAgentConfig{Planner: historyReplayPlanner{}}))
}

func (historyReplayPlanner) PlanStart(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
	if err := measureHistoryRequest(ctx, input.Agent, input.Messages); err != nil {
		return nil, err
	}
	return &planner.PlanResult{
		ToolCalls: []planner.ToolRequest{{Name: workflow.Draft, ToolCallID: "history-draft", Payload: rawjson.Message(`{"topic":"history replay"}`)}},
		Await:     planner.NewAwait(planner.AwaitClarificationItem(&planner.AwaitClarification{ID: "history-clarification", Question: workflowHistoryQuestion})),
	}, nil
}

func (historyReplayPlanner) PlanResume(ctx context.Context, input *planner.PlanResumeInput) (*planner.PlanResult, error) {
	if err := measureHistoryRequest(ctx, input.Agent, input.Messages); err != nil {
		return nil, err
	}
	seenRecall := false
	for _, result := range input.ToolOutputs {
		seenRecall = seenRecall || result.Name == tools.RecallContext
	}
	if !seenRecall && input.Finalize == nil {
		if err := validateHistoryClarification(input.Messages); err != nil {
			return nil, err
		}
		return &planner.PlanResult{ToolCalls: []planner.ToolRequest{
			{Name: delegated.Summarize, ToolCallID: "history-child", Payload: rawjson.Message(`{"topic":"history replay"}`)},
			{Name: tools.RecallContext, ToolCallID: "history-recall", Payload: rawjson.Message(`{"read":{"id":"1","part":"result","offset":0}}`)},
		}}, nil
	}
	return &planner.PlanResult{FinalResponse: &planner.FinalResponse{Message: &model.Message{
		Role:  model.ConversationRoleAssistant,
		Parts: []model.Part{model.TextPart{Text: "history replay complete"}},
	}}}, nil
}

// assertHistoryToolContent checks protocol 5 references and typed activity boundaries.
func assertHistoryToolContent(t *testing.T, history *historypb.History, protocol int) {
	t.Helper()
	schedules := make(map[int64]string)
	counts := make(map[string]int)
	seedCalls, resultBatches, toolCompletions, childValidations := 0, 0, 0, 0
	var childHead string
	childHeadAdopted := false
	plannerSteps := 0
	resumeSchedules := 0
	confirmationSignals := 0
	wantSteps, wantTools, wantBatches := 2, 2, 1
	if protocol >= 8 {
		wantSteps, wantTools, wantBatches = 3, 3, 2
	}
	if protocol >= 11 {
		wantBatches++ // The clarification question has its own RecordContent activity.
	}
	var finalTranscript runstore.Hash
	var sealInput *api.SealRecordInput
	for _, event := range history.Events {
		if signal := event.GetWorkflowExecutionSignaledEventAttributes(); signal != nil && signal.SignalName == api.SignalProvideConfirmation {
			confirmationSignals++
		}
		if scheduled := event.GetActivityTaskScheduledEventAttributes(); scheduled != nil {
			name := scheduled.ActivityType.Name
			schedules[event.EventId] = name
			counts[name]++
			data := scheduled.Input.Payloads[0].Data
			switch name {
			case "runtime.publish_hook":
				for _, input := range historyHookInputs(t, data) {
					if input.Type == hooks.PlannerStep {
						plannerSteps++
						var step hooks.PlannerStepEvent
						require.NoError(t, json.Unmarshal(input.Payload, &step))
						if protocol < 9 {
							assert.Equal(t, plannerSteps, step.Step)
						}
						assert.Equal(t, protocol, step.ProtocolVersion)
						assert.Equal(t, step.IndexHead, step.ViewRef.IndexHead)
						assert.Nil(t, step.ViewRef.Pending)
					}
				}
			case agentsruntime.RecordContentActivityName(coordinator.AgentID):
				var input api.RecordContentInput
				require.NoError(t, json.Unmarshal(data, &input))
				assert.Equal(t, "run-protocol-history", input.RootRunID)
				if input.SeedMessages {
					seedCalls++
					switch {
					case protocol >= 9:
						require.Len(t, input.Items, 5)
					case protocol >= 8:
						require.Len(t, input.Items, 3)
					default:
						require.Len(t, input.Items, 1)
					}
					var message model.Message
					require.NoError(t, json.Unmarshal(input.Items[0], &message))
					assert.Equal(t, "record this request", messageText(&message))
				} else {
					resultBatches++
					require.NotEmpty(t, input.Items)
				}
			case agentsruntime.RecallActivityName(coordinator.AgentID):
				var input api.RecallActivityInput
				require.NoError(t, json.Unmarshal(data, &input))
				assert.Equal(t, "run-protocol-history", input.RunID)
				assert.Equal(t, "run-protocol-history", input.Scope)
				assert.NotNil(t, input.ViewRef.Checkpoint)
				assert.Equal(t, tools.RecallContext, input.Call.Name)
			case agentsruntime.ChildDispatchActivityName(coordinator.AgentID):
				var input api.ChildDispatchInput
				require.NoError(t, json.Unmarshal(data, &input))
				assert.Equal(t, delegated.Summarize, input.Call.Name)
				assert.Equal(t, "history-child", input.Call.ToolCallID)
				if protocol >= 8 {
					assert.NotNil(t, input.View.Checkpoint, "child admission must load a compacted parent view")
					assert.Greater(t, input.View.TailFrom, index.MessageID(1))
				}
				assert.Equal(t, "run-protocol-history", input.ParentRun.RootRunID)
				assert.NotEmpty(t, input.View.IndexHead)
				require.Len(t, input.View.Pending, 1)
				require.Len(t, input.View.Pending[0].Calls, 2)
				assert.NotContains(t, string(data), "record this request")
			case "features.coordinator.executetool":
				var input api.ToolInput
				require.NoError(t, json.Unmarshal(data, &input))
				assert.Equal(t, "run-protocol-history", input.RootRunID)
			case "features.coordinator.plan", "features.coordinator.resume":
				var input api.PlanActivityInput
				require.NoError(t, json.Unmarshal(data, &input))
				assert.NotEmpty(t, input.View.IndexHead)
				var fields map[string]jsontext.Value
				require.NoError(t, json.Unmarshal(data, &fields))
				for _, removed := range []string{"Messages", "ToolOutputs", "TypedInputs"} {
					assert.NotContains(t, fields, removed)
				}
				if protocol >= 6 {
					wantStep := 1
					if name == "features.coordinator.resume" {
						resumeSchedules++
						wantStep = 1 + resumeSchedules
					}
					if protocol >= 9 {
						wantStep = counts["features.coordinator.plan"] + counts["features.coordinator.resume"]
					}
					assert.Equal(t, wantStep, input.Step)
				}
				if name == "features.coordinator.plan" {
					assert.Empty(t, input.View.Pending)
				} else {
					if protocol >= 7 {
						require.Len(t, input.Models, 1)
						assert.Equal(t, "history", input.Models[0].Model)
						expectedBudget := 6000
						if protocol >= 15 {
							expectedBudget = 5000
						}
						assert.Equal(t, expectedBudget, input.Models[0].Budget)
					}
					assert.NotEmpty(t, input.View.Pending)
					if childHead != "" && !childHeadAdopted {
						assert.Equal(t, childHead, string(input.View.IndexHead), "resume adopts the head committed by child validation")
						for _, entry := range input.View.Pending {
							assert.NotEqual(t, "assistant", string(entry.Kind), "committed assistant entries are no longer pending")
						}
						childHeadAdopted = true
					}
				}
			case agentsruntime.SealRecordActivityName(coordinator.AgentID):
				var input api.SealRecordInput
				require.NoError(t, json.Unmarshal(data, &input))
				sealInput = &input
				assert.Equal(t, "run-protocol-history", input.RootRunID)
				assert.Equal(t, "run-protocol-history", input.RunID)
				assert.Equal(t, "run_sealed:run-protocol-history", input.EventKey)
				assert.Equal(t, protocol, input.ProtocolVersion)
				assert.False(t, input.Timestamp.IsZero())
				require.Len(t, input.Pending, 1)
				assert.Equal(t, index.Assistant, input.Pending[0].Kind)
				assert.Equal(t, finalTranscript, input.Pending[0].Hash)
			case agentsruntime.FinishRunActivityName(coordinator.AgentID):
				var input api.FinishRunInput
				require.NoError(t, json.Unmarshal(data, &input))
				require.Len(t, input.ToolEvents, wantTools)
				assert.NotEmpty(t, input.ToolEvents[0].Refs.Result)
				assert.Empty(t, input.ToolEvents[0].Result)
				assert.NotNil(t, input.ToolEvents[1].Error)
			}
		}
		if completed := event.GetActivityTaskCompletedEventAttributes(); completed != nil {
			if len(completed.GetResult().GetPayloads()) == 0 {
				continue
			}
			data := completed.Result.Payloads[0].Data
			switch schedules[completed.ScheduledEventId] {
			case "features.coordinator.executetool":
				toolCompletions++
				var output api.ToolOutput
				require.NoError(t, json.Unmarshal(data, &output, jsonopts.FormatDurationAsNano(true)))
				assert.NotEmpty(t, output.Refs.Result)
				assert.NotEmpty(t, output.Refs.Payload)
				assert.Empty(t, output.Payload)
			case agentsruntime.ChildDispatchActivityName(coordinator.AgentID):
				childValidations++
				var output api.ChildDispatchOutput
				require.NoError(t, json.Unmarshal(data, &output, jsonopts.FormatDurationAsNano(true)))
				require.NotNil(t, output.RetryHint)
				assert.Equal(t, "topic requires evidence", output.Error)
				assert.NotEmpty(t, output.IndexHead)
				childHead = string(output.IndexHead)
			case "features.coordinator.plan":
				var output api.PlanActivityOutput
				require.NoError(t, json.Unmarshal(data, &output, jsonopts.FormatDurationAsNano(true)))
				assert.NotEmpty(t, output.IndexHead)
				if protocol >= 7 {
					require.Len(t, output.Facts, 1)
					assert.Equal(t, output.CallKeys[0], output.Facts[0].Call)
					assert.True(t, output.Facts[0].ViewBearing)
				}
				switch {
				case protocol >= 9 && output.ContextOverflow != nil:
					assert.Nil(t, output.Result)
					assert.Empty(t, output.ToolCallRefs)
					assert.Equal(t, "provider", output.ContextOverflow.Source)
				case protocol >= 8:
					require.Len(t, output.ToolCallRefs, 1)
				default:
					require.Len(t, output.ToolCallRefs, 2)
				}
			case "features.coordinator.resume":
				var output api.PlanActivityOutput
				require.NoError(t, json.Unmarshal(data, &output, jsonopts.FormatDurationAsNano(true)))
				assert.NotEmpty(t, output.IndexHead)
				if protocol >= 6 {
					assert.NotEmpty(t, output.TranscriptRef)
					finalTranscript = output.TranscriptRef
					if protocol >= 7 {
						require.NotNil(t, output.View)
						require.Len(t, output.Facts, 1)
						assert.Equal(t, output.CallKeys[0], output.Facts[0].Call)
					}
				}
			}
		}
		if completed := event.GetWorkflowExecutionCompletedEventAttributes(); completed != nil {
			var output api.RunOutput
			require.NoError(t, json.Unmarshal(completed.Result.Payloads[0].Data, &output))
			assert.NotEmpty(t, output.ToolEventsRef)
			assert.NotEmpty(t, output.FinalRef)
			assert.Equal(t, wantTools, output.ToolEventCount)
		}
		assert.Nil(t, event.GetStartChildWorkflowExecutionInitiatedEventAttributes(), "rejected validation must not start a child")
	}
	if protocol >= 6 {
		require.NotNil(t, sealInput)
		assert.Equal(t, 1, counts[agentsruntime.SealRecordActivityName(coordinator.AgentID)])
		assert.Equal(t, wantSteps, plannerSteps)
	}
	assert.Equal(t, 1, counts[agentsruntime.FinishRunActivityName(coordinator.AgentID)])
	assert.Equal(t, 1, seedCalls)
	assert.Equal(t, wantBatches, resultBatches)
	assert.Equal(t, 1, toolCompletions)
	assert.Equal(t, 1, childValidations)
	assert.True(t, childHeadAdopted, "a subsequent resume must adopt the child-validation head")
	if protocol >= 10 {
		assert.Equal(t, 1, confirmationSignals)
		assert.Equal(t, 2, counts["features.coordinator.resume"], "one resume after confirmation and one after its chosen tools")
	}
	if protocol >= 8 {
		assert.Equal(t, 1, counts[agentsruntime.RecallActivityName(coordinator.AgentID)])
	}
}

// assertHistoryClarification proves the recorded history includes RB3's content
// command and adjacent question/answer entries in a checkpoint-bearing resume.
func assertHistoryClarification(t *testing.T, history *historypb.History) {
	t.Helper()
	question, err := json.Marshal(&model.Message{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: workflowHistoryQuestion}}})
	require.NoError(t, err)
	signals, questionWrites, exchanges := 0, 0, 0
	for _, event := range history.Events {
		if signal := event.GetWorkflowExecutionSignaledEventAttributes(); signal != nil && signal.SignalName == api.SignalProvideClarification {
			signals++
		}
		scheduled := event.GetActivityTaskScheduledEventAttributes()
		if scheduled == nil {
			continue
		}
		data := scheduled.Input.Payloads[0].Data
		switch scheduled.ActivityType.Name {
		case agentsruntime.RecordContentActivityName(coordinator.AgentID):
			var input api.RecordContentInput
			require.NoError(t, json.Unmarshal(data, &input))
			for _, item := range input.Items {
				if bytes.Equal(item, question) {
					questionWrites++
				}
			}
		case "features.coordinator.resume":
			var input api.PlanActivityInput
			require.NoError(t, json.Unmarshal(data, &input))
			for i, entry := range input.View.Pending {
				if entry.Kind != index.Clarification {
					continue
				}
				exchanges++
				require.Positive(t, i)
				prompt := input.View.Pending[i-1]
				assert.Equal(t, index.Message, prompt.Kind)
				assert.Equal(t, model.ConversationRoleAssistant, prompt.Role)
				assert.Equal(t, len(workflowHistoryQuestion), prompt.Bytes)
				assert.NotEmpty(t, prompt.Hash)
				assert.Equal(t, len(workflowHistoryAnswer), entry.Bytes)
				assert.NotNil(t, input.View.Checkpoint)
			}
		}
	}
	assert.Equal(t, 1, signals)
	assert.Equal(t, 1, questionWrites)
	assert.Equal(t, 1, exchanges)
}

// validateHistoryClarification checks the real recorder's first resume, before
// its model call is followed by child validation and context lookup.
func validateHistoryClarification(messages []*model.Message) error {
	if err := transcript.ValidateView(messages, transcript.GrammarBedrock{}); err != nil {
		return err
	}
	if err := transcript.ValidateView(messages, transcript.GrammarAnthropic{}); err != nil {
		return err
	}
	if len(messages) < 2 || messageText(messages[len(messages)-2]) != workflowHistoryQuestion || messageText(messages[len(messages)-1]) != workflowHistoryAnswer {
		return fmt.Errorf("recorded resume must end with the clarification question and answer")
	}
	return nil
}

// historyHookInputs expands the ordered batch without treating its command as an event.
func historyHookInputs(t *testing.T, data []byte) []*api.HookActivityInput {
	t.Helper()
	var input api.HookActivityInput
	require.NoError(t, json.Unmarshal(data, &input))
	if input.Type != "runtime.publish_hook_batch" {
		return []*api.HookActivityInput{&input}
	}
	var inputs []*api.HookActivityInput
	require.NoError(t, json.Unmarshal(input.Payload, &inputs))
	require.GreaterOrEqual(t, len(inputs), 2)
	assert.LessOrEqual(t, len(inputs), 3)
	assert.Contains(t, []hooks.EventType{hooks.PlannerStep, hooks.ContextOverflow}, inputs[0].Type)
	for _, input := range inputs[1:] {
		assert.Contains(t, []hooks.EventType{hooks.RunPhaseChanged, hooks.ContextCompacted, hooks.ContextCompactionSkipped}, input.Type)
	}
	return inputs
}

// measureHistoryRequest exercises recorded facts and a fixed-overhead threshold
// compaction without depending on worker model configuration during replay.
func measureHistoryRequest(ctx context.Context, agent planner.PlannerContext, view []*model.Message) error {
	client, ok := agent.ModelClient("history")
	if !ok {
		return fmt.Errorf("history model is not registered")
	}
	messages := make([]*model.Message, 0, 1+len(view))
	messages = append(messages, &model.Message{Role: model.ConversationRoleSystem, Parts: []model.Part{model.TextPart{Text: strings.Repeat("fixed overhead ", 40)}}})
	messages = append(messages, view...)
	_, err := client.Complete(ctx, &model.Request{Model: "history", Messages: messages})
	return err
}
