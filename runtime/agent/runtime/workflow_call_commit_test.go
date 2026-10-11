package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanActivityCallOutput(t *testing.T) {
	rt, _, input := newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
		client, ok := agent.ModelClient("registered-model")
		require.True(t, ok)
		for i := range 2 {
			_, err := client.Complete(ctx, &model.Request{Messages: []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: fmt.Sprint(i)}}}}})
			if err != nil {
				return err
			}
		}
		return nil
	})
	rt.models["registered-model"] = &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "answer"}}}}, StopReason: "end_turn"}}
	input.Step = 7
	out, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 2), input)
	require.NoError(t, err)
	require.Len(t, out.CallKeys, 2)
	assert.Equal(t, &out.CallKeys[1], out.ProducingCall)
	require.NotNil(t, out.RequestGrowth)
	assert.Equal(t, out.CallKeys[0], out.RequestGrowth.Key)
	calls, err := rt.RunStore.ListCalls(t.Context(), "root-run", "child-run")
	require.NoError(t, err)
	for _, call := range calls {
		started, err := callrecord.DecodeStarted(call.Started)
		require.NoError(t, err)
		assert.Equal(t, 7, started.Step)
	}
}

func TestWorkflowCallCommitSurvivesEventInterception(t *testing.T) {
	evidence := New()
	recording := NewCallRecording(evidence.RunStore, "root", index.ViewRef{}, nil, 1)
	client := &callRecordingClient{inner: &historyCountingClient{completeText: "recorded"}, recording: recording}
	_, err := client.Complete(recordingCallContext(1), &model.Request{})
	require.NoError(t, err)
	childContext := model.WithCallInfo(t.Context(), model.CallInfo{RootRunID: "root", RunID: "child", Seq: 1, Attempt: 1})
	childRecording := NewCallRecording(evidence.RunStore, "root", index.ViewRef{}, nil, 1)
	childClient := &callRecordingClient{inner: &historyCountingClient{completeText: "child answer"}, recording: childRecording}
	childResponse, err := childClient.Complete(childContext, &model.Request{})
	require.NoError(t, err)
	var baseline *runrecord.RunRecord
	for _, mode := range []string{"none", "drop", "rewrite"} {
		t.Run(mode, func(t *testing.T) {
			rt := New(WithRunStore(evidence.RunStore))

			intercepted := 0
			rt.interceptors = []Interceptor{RuntimeInterceptorFuncs{BeforeEventFunc: func(_ context.Context, input *BeforeEventInput) (*BeforeEventDecision, error) {
				intercepted++
				if mode == "none" {
					return nil, nil
				}
				if mode == "rewrite" {
					return &BeforeEventDecision{Event: hooks.NewUsageEvent(input.Event.RunID(), "agent", "", model.TokenUsage{})}, nil
				}
				return &BeforeEventDecision{Drop: true}, nil
			}}}
			require.NoError(t, rt.publishHook(t.Context(), hooks.NewChildRunLinkedEvent("run", "agent", "", "child.tool", "call", "child", "child-agent"), "turn"))
			require.NoError(t, rt.publishHook(t.Context(), hooks.NewPlannerStepEvent("child", "child-agent", "", 1, index.ViewRef{}, childRecording.Keys(), api.WorkflowProtocolVersion, time.Unix(1, 0)), "turn"))
			childData, err := callrecord.Encode(childResponse.Content)
			require.NoError(t, err)
			childHash, err := rt.RunStore.Put(t.Context(), "root", childData)
			require.NoError(t, err)
			childSegment, err := index.NewSegment("", "", []index.MessageEntry{{ID: 1, Kind: index.Assistant, Hash: childHash}})
			require.NoError(t, err)
			_, err = rt.SealRecordActivity(t.Context(), &api.SealRecordInput{ProtocolVersion: api.WorkflowProtocolVersion, RootRunID: "root", RunID: "child", Pending: childSegment.Entries, EventKey: "run_sealed:child", Timestamp: time.Unix(2, 0)})
			require.NoError(t, err)
			key := runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}
			base := &workflowPlanInput{protocolVersion: api.WorkflowProtocolVersion, AgentID: "agent", RunContext: run.Context{RootRunID: "root", RunID: "run"}}
			wf := &testWorkflowContext{ctx: t.Context(), finishRuntime: rt, hookRuntime: rt, plannerOutputs: []*api.PlanActivityOutput{
				{Result: &planner.PlanResult{ToolCalls: []planner.ToolRequest{{Name: "test.lookup", ToolCallID: "call", Payload: rawjson.Message(`{}`)}}}, CallKeys: []runstore.CallKey{key}, ProducingCall: &key, RequestGrowth: &api.RequestGrowth{Key: key, Outside: 99}},
				{Result: assistantCommitResult(), CallKeys: []runstore.CallKey{key}, ProducingCall: &key, RequestGrowth: &api.RequestGrowth{Key: key, Outside: 100}},
			}}
			input := PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext}
			for range 2 {
				input.View = base.view()
				out, err := rt.runPlanActivity(wf, base, "plan", engine.ActivityOptions{}, input, time.Time{})
				require.NoError(t, err)
				base.adopt(out)
				require.NoError(t, base.recordAssistant(out.TranscriptRef, out.Result.ToolCalls))
			}
			_, sealErr := rt.SealRecordActivity(t.Context(), &api.SealRecordInput{ProtocolVersion: api.WorkflowProtocolVersion, RootRunID: "root", RunID: "run", IndexHead: base.IndexHead, Pending: base.view().Pending, EventKey: "run_sealed:run", Timestamp: wf.Now()})
			require.NoError(t, sealErr)
			assert.Equal(t, 1, wf.plannerCalls[0].Input.Step)
			assert.Equal(t, 2, wf.plannerCalls[1].Input.Step)
			events, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
			require.NoError(t, err)
			var steps []hooks.PlannerStepEvent
			for _, event := range events.Events {
				if event.Type == hooks.PlannerStep {
					var step hooks.PlannerStepEvent
					require.NoError(t, json.Unmarshal(event.Payload, &step))
					assert.Equal(t, fmt.Sprintf("planner_step:run:%d", step.Step), event.EventKey)
					assert.Nil(t, step.ViewRef.Pending)
					assert.Equal(t, step.IndexHead, step.ViewRef.IndexHead)
					assert.Equal(t, []runstore.CallKey{key}, step.CallKeys)
					steps = append(steps, step)
				}
			}
			assert.Len(t, steps, 2)
			assert.Equal(t, 1, intercepted, "only first growth event is interceptable")
			assert.Len(t, base.Entries, 2)
			for _, entry := range base.Entries {
				assert.Equal(t, &key, entry.Call)
			}
			view := testView(t, rt, base)
			assert.Len(t, view.Messages, 2)
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
			require.NoError(t, err)
			require.Len(t, record.Runs, 2, "canonical child link survives every event interceptor")
			assert.Equal(t, "child", record.Runs[1].RunID)
			assert.Equal(t, []*model.Message{&childResponse.Content[0]}, record.Runs[1].Transcript)
			require.Len(t, record.Runs[1].Calls, 1)
			assert.Equal(t, runrecord.Adopted, record.Runs[1].Calls[0].Class)
			assert.True(t, record.Runs[1].Sealed)
			exported, err := record.Export().Marshal()
			require.NoError(t, err)
			restored, err := runrecord.FromExport(exported)
			require.NoError(t, err)
			assert.Equal(t, record, restored)
			assert.Equal(t, view.Messages, record.Runs[0].Transcript)
			require.Len(t, record.Runs[0].Calls, 1)
			assert.Equal(t, runrecord.Adopted, record.Runs[0].Calls[0].Class)
			assert.True(t, record.Runs[0].Sealed)
			// Observer payloads intentionally vary. All reconstructed evidence,
			// content, transcript and classifications must remain identical.
			for i := range record.Runs {
				record.Runs[i].Events = nil
			}
			if baseline == nil {
				baseline = record
			} else {
				assert.Equal(t, baseline, record)
			}
		})
	}
}

func TestWorkflowRecoverySteps(t *testing.T) {
	rt := New()
	base := &workflowPlanInput{protocolVersion: api.WorkflowProtocolVersion, RunContext: run.Context{RootRunID: "root", RunID: "run", Attempt: 1}}
	hash, err := rt.RunStore.Put(t.Context(), "root", []byte("retry"))
	require.NoError(t, err)
	wf := &testWorkflowContext{ctx: t.Context(), finishRuntime: rt, hookRuntime: rt, plannerOutputs: []*api.PlanActivityOutput{
		{Recovery: &ModelRecovery{Kind: model.OutputValidationOutputBounds, CorrectionRef: hash, DisableTools: true, Attempt: 1}},
		{Result: assistantCommitResult()},
		{Result: assistantCommitResult()},
	}}
	caps := policy.CapsState{RemainingRecoveryTurns: 2}
	next := 2
	input := PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext}
	_, err = rt.runPlanActivityRecovering(wf, base, "plan", engine.ActivityOptions{}, input, time.Time{}, &caps, &next)
	require.NoError(t, err)
	input.Finalize = &planner.Termination{}
	_, err = rt.runPlanActivityRecovering(wf, base, "resume", engine.ActivityOptions{}, input, time.Time{}, &caps, &next)
	require.NoError(t, err)
	for i, call := range wf.plannerCalls {
		assert.Equal(t, i+1, call.Input.Step)
	}
}

func TestFinalAssistantEntry(t *testing.T) {
	rt := New()
	key := runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}
	base := &workflowPlanInput{protocolVersion: api.WorkflowProtocolVersion, AgentID: "agent", RunContext: run.Context{RootRunID: "root", RunID: "run"}}
	out, err := rt.storePlanActivityOutput(t.Context(), "root", &PlanActivityOutput{Result: assistantCommitResult(), ProducingCall: &key}, []*model.Message{assistantCommitResult().FinalResponse.Message}, nil)
	require.NoError(t, err)
	base.adopt(out)
	st := newRunLoopState(out.Result, out.TranscriptRef, model.TokenUsage{}, policy.CapsState{}, 2, toolPolicyEnvelope{})
	wf := &testWorkflowContext{ctx: t.Context(), finishRuntime: rt, hookRuntime: rt}
	_, err = rt.finishWithoutToolCalls(wf.Context(), &RunInput{AgentID: "agent", RunID: "run", RootRunID: "root"}, base, st, "turn")
	require.NoError(t, err)
	require.Len(t, base.Entries, 1)
	assert.Equal(t, index.Assistant, base.Entries[0].Kind)
	assert.Equal(t, &key, base.Entries[0].Call)
	assert.Equal(t, out.TranscriptRef, base.Entries[0].Hash)
	view := testView(t, rt, base)
	assert.Equal(t, []*model.Message{assistantCommitResult().FinalResponse.Message}, view.Messages)
}

func TestProducingCallSkipsAuxiliaryWithoutEvents(t *testing.T) {
	var rt *Runtime
	var input *PlanActivityInput
	rt, _, input = newCallInfoRuntime(t, func(ctx context.Context, agent planner.PlannerContext) error {
		client, ok := agent.ModelClient("registered-model")
		require.True(t, ok)
		_, err := client.Complete(ctx, &model.Request{})
		if err != nil {
			return err
		}
		rt.models["registered-model"] = &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{StopReason: "end_turn"}}
		auxiliary, ok := agent.ModelClient("registered-model")
		require.True(t, ok)
		_, err = auxiliary.Complete(ctx, &model.Request{})
		return err
	})
	rt.models["registered-model"] = &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "producing"}}}}, StopReason: "end_turn"}}
	out, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), input)
	require.NoError(t, err)
	require.Len(t, out.CallKeys, 2)
	assert.Equal(t, &out.CallKeys[0], out.ProducingCall)
}

func TestWorkflowRequestGrowthOnce(t *testing.T) {
	rt := New()
	base := &workflowPlanInput{protocolVersion: api.WorkflowProtocolVersion, RunContext: run.Context{RootRunID: "root", RunID: "run"}}
	key := runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}
	wf := &testWorkflowContext{ctx: t.Context(), finishRuntime: rt, hookRuntime: rt, plannerOutputs: []*PlanActivityOutput{
		{Result: assistantCommitResult(), RequestGrowth: &api.RequestGrowth{Key: key, Inside: 100, Outside: 101}},
		{Result: assistantCommitResult(), RequestGrowth: &api.RequestGrowth{Key: key, Inside: 100, Outside: 1000}},
	}}
	for range 2 {
		_, err := rt.runPlanActivity(wf, base, "plan", engine.ActivityOptions{}, PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext}, time.Time{})
		require.NoError(t, err)
	}
	page, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
	require.NoError(t, err)
	var growth []hooks.ContextRequestGrowthEvent
	for _, event := range page.Events {
		if event.Type == hooks.ContextRequestGrowth {
			var decoded hooks.ContextRequestGrowthEvent
			require.NoError(t, json.Unmarshal(event.Payload, &decoded))
			growth = append(growth, decoded)
		}
	}
	require.Len(t, growth, 1)
	assert.Equal(t, key, growth[0].Key)
	assert.Equal(t, 100, growth[0].Inside)
	assert.Equal(t, 101, growth[0].Outside)
}

func TestPlanActivityStoresUnaryFinalTranscript(t *testing.T) {
	rt := New()
	rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{start: func(context.Context, *planner.PlanInput) (*planner.PlanResult, error) {
		return assistantCommitResult(), nil
	}}}
	out, err := rt.PlanStartActivity(engine.WithActivityAttempt(t.Context(), 1), &PlanActivityInput{AgentID: "agent", RunID: "run", Step: 1, RunContext: run.Context{RootRunID: "root", RunID: "run"}})
	require.NoError(t, err)
	require.NotEmpty(t, out.TranscriptRef)
	assert.Equal(t, []*model.Message{assistantCommitResult().FinalResponse.Message}, testStoredTranscript(t, rt, out, "root"))
}

func TestPlannerStepIncludedInInputBudget(t *testing.T) {
	rt := New()
	base := &workflowPlanInput{protocolVersion: api.WorkflowProtocolVersion, step: 99}
	input := PlanActivityInput{RunID: "run", RunContext: run.Context{RootRunID: "root", RunID: "run", Labels: map[string]string{"data": ""}}}
	data, err := json.Marshal(input)
	require.NoError(t, err)
	input.RunContext.Labels["data"] = strings.Repeat("x", maxPlanActivityInputBytes-len(data))
	require.NoError(t, enforcePlanActivityInputBudget(input))
	wf := &testWorkflowContext{ctx: t.Context(), finishRuntime: rt, plannerOutputs: []*PlanActivityOutput{{Result: assistantCommitResult()}}}
	_, err = rt.runPlanActivity(wf, base, "plan", engine.ActivityOptions{}, input, time.Time{})
	var oversized *PlanActivityInputBudgetError
	require.ErrorAs(t, err, &oversized)
	assert.Empty(t, wf.plannerCalls)
	assert.Equal(t, 99, base.step)
}

func assistantCommitResult() *planner.PlanResult {
	result := oracleFinal()
	result.FinalResponse.Message.Role = model.ConversationRoleAssistant
	return result
}
