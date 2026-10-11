package runtime

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/callrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	storeinmem "github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore/inmem"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextCompactionOption(t *testing.T) {
	config := ContextCompaction{TriggerAt: contextmeasure.Rate{Tokens: 80, Bytes: 100}, KeepResultTools: []tools.Ident{"test.keep"}}
	rt := New(WithContextCompaction("agent", config))
	config.KeepResultTools[0] = "changed"
	input := RunInput{AgentID: "agent", ContextCompaction: &ContextCompaction{KeepResultTools: []tools.Ident{"caller"}}}
	rt.stampContextCompaction(&input)
	require.NotNil(t, input.ContextCompaction)
	assert.Equal(t, []tools.Ident{"test.keep"}, input.ContextCompaction.KeepResultTools)
	assert.Equal(t, contextmeasure.Rate{Tokens: 80, Bytes: 100}, input.ContextCompaction.TriggerAt)
	reg := AgentRegistration{ID: "agent", Policy: RunPolicy{History: func(context.Context, []*model.Message, []*model.ToolDefinition) ([]*model.Message, error) {
		return nil, nil
	}}}
	require.ErrorContains(t, rt.RegisterAgent(t.Context(), reg), "HistoryPolicy")
	rt.stampContextCompaction(&RunInput{AgentID: "off"})
	assert.Panics(t, func() {
		New(WithContextCompaction("agent", ContextCompaction{TriggerAt: contextmeasure.Rate{Tokens: 101, Bytes: 100}}))
	})
}

func TestCompactionFactsBoundary(t *testing.T) {
	store := storeinmem.New()
	view := []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "hello"}}}}
	recorder := NewCallRecording(store, "root", index.ViewRef{}, view, 1)
	for seq := 1; seq <= 2; seq++ {
		ctx := model.WithCallInfo(t.Context(), model.CallInfo{RunID: "run", RootRunID: "root", Seq: seq, Attempt: 1, ModelID: "fake"})
		client := &callRecordingClient{inner: &historyCountingClient{completeText: "response"}, recording: recorder}
		_, err := client.Complete(ctx, &model.Request{Model: "fake", Messages: view})
		require.NoError(t, err)
	}
	facts := recorder.Facts()
	require.Len(t, facts, 2)
	calls, err := store.ListCalls(t.Context(), "root", "run")
	require.NoError(t, err)
	for i, call := range calls {
		completion, err := callrecord.DecodeCompletion(call.Completion)
		require.NoError(t, err)
		assert.Equal(t, completion.Facts, &facts[i])
	}
	for i, key := range recorder.Keys() {
		assert.Equal(t, key, facts[i].Call)
		assert.True(t, facts[i].ViewBearing)
	}
	state := compaction.State{Models: map[string]compaction.ModelState{"z": {Rate: contextmeasure.Rate{Tokens: 1, Bytes: 3}, Budget: 5000}, "a": {Budget: 4000}}}
	models := compactionModels(state)
	require.Len(t, models, 2)
	assert.Equal(t, "a", models[0].Model)
	assert.Equal(t, "z", models[1].Model)
	facts[0].Call = runstore.CallKey{}
	assert.NotEqual(t, facts, recorder.Facts())
}

func TestWorkflowCompactionReconstruction(t *testing.T) {
	rt := New()
	base := compactionFixture(t, rt.RunStore, 30)
	config := &ContextCompaction{TriggerAt: contextmeasure.Rate{Tokens: 1, Bytes: 100}, KeepRecentTokens: 30, ProtectRecentToolTokens: 40, MinSavingTokens: 20}
	base.compaction = config
	base.protocolVersion = api.WorkflowProtocolVersion
	base.AgentID = "agent"
	base.RunContext = run.Context{RootRunID: "root", RunID: "run", Attempt: 1}
	provider := &recordingFactsClient{recordingTestClient: &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "answer"}}}}, StopReason: "end_turn"}}, limits: model.ContextLimits{ContextWindow: 6000}, resolved: "measured"}
	require.NoError(t, rt.RegisterModel("fake", provider))
	var counts []int
	rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{start: func(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
		counts = append(counts, len(input.Messages))
		client, ok := input.Agent.ModelClient("fake")
		require.True(t, ok)
		_, err := client.Complete(ctx, &model.Request{Model: "fake", Messages: input.Messages})
		if err != nil {
			return nil, err
		}
		return assistantCommitResult(), nil
	}}}
	wf := &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}
	for step := 0; step < 8; step++ {
		if step == 4 {
			provider.limits.ContextWindow = 4000
		}
		input := PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}
		if step == 7 {
			input.Finalize = &planner.Termination{}
		}
		out, err := rt.runPlanActivity(wf, base, "plan", engine.ActivityOptions{}, input, time.Time{})
		require.NoError(t, err)
		base.adopt(out)
		if step < 7 {
			require.NoError(t, base.recordAssistant(out.TranscriptRef, nil))
			message := &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: strings.Repeat("growth ", 150)}}}
			data, err := json.Marshal(message)
			require.NoError(t, err)
			hash, err := rt.RunStore.Put(t.Context(), "root", data)
			require.NoError(t, err)
			size, err := contextmeasure.MeasureMessages([]*model.Message{message})
			require.NoError(t, err)
			require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Message, Hash: hash, Bytes: size.Bytes, Role: size.Role}))
		}
	}
	assert.Equal(t, 61, counts[0], "first turn receives all history")
	assert.Less(t, counts[1], counts[0])
	assert.NotNil(t, wf.plannerCalls[7].Input.Finalize)
	// A durable but unadopted tiny-budget call must not disable the recorded run.
	provider.limits.ContextWindow = 1
	orphanView := base.view()
	loaded, err := rt.loadTestView(t.Context(), "root", "run", orphanView)
	require.NoError(t, err)
	seq, err := rt.RunStore.NextSeq(t.Context(), "root", "run")
	require.NoError(t, err)
	recorder := NewCallRecording(rt.RunStore, "root", orphanView, loaded.Messages, 9)
	orphanClient := &callRecordingClient{inner: provider, recording: recorder}
	ctx := model.WithCallInfo(t.Context(), model.CallInfo{RunID: "run", RootRunID: "root", Seq: seq, Attempt: 1, ModelID: "fake"})
	_, err = orphanClient.Complete(ctx, &model.Request{Model: "fake", Messages: loaded.Messages})
	require.NoError(t, err)
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
	require.NoError(t, err, "compacted requests must reconstruct with matching digests")
	rr := record.Runs[0]
	require.NotNil(t, rr.CompactionSettings)
	assert.Equal(t, pureCompactionSettings(config), *rr.CompactionSettings)
	var state compaction.State
	var previous index.ViewRef
	byKey := make(map[runstore.CallKey]runrecord.Call)
	for _, call := range rr.Calls {
		byKey[call.Record.Key] = call
	}
	source := func(_ context.Context, h runstore.Hash) ([]byte, error) {
		data, ok := record.Content[h]
		if !ok {
			return nil, fmt.Errorf("missing %s", h)
		}
		return data, nil
	}
	transitions := 0
	for _, event := range rr.Events {
		if event.Type != hooks.PlannerStep {
			continue
		}
		var step hooks.PlannerStepEvent
		require.NoError(t, json.Unmarshal(event.Payload, &step))
		entries, err := index.Walk(t.Context(), source, step.IndexHead)
		require.NoError(t, err)
		before := previous
		before.IndexHead = step.IndexHead
		decision := compaction.Decide(state, entries, before, *rr.CompactionSettings)
		assert.Equal(t, decision.View.TailFrom, step.ViewRef.TailFrom)
		assert.Equal(t, decision.View.ClearedThrough, step.ViewRef.ClearedThrough)
		assert.Equal(t, decision.View.Budget, step.ViewRef.Budget)
		assert.GreaterOrEqual(t, step.ViewRef.TailFrom, previous.TailFrom)
		assert.GreaterOrEqual(t, step.ViewRef.ClearedThrough, previous.ClearedThrough)
		if step.ViewRef.TailFrom > previous.TailFrom {
			transitions++
		}
		state = decision.State
		if step.ViewRef.Checkpoint != nil {
			state.Rendered = compaction.RenderedCheckpoint{Hash: string(*step.ViewRef.Checkpoint), TailFrom: step.ViewRef.TailFrom, Budget: step.ViewRef.Budget, Bytes: step.CheckpointBytes}
		}
		facts := make([]contextmeasure.RequestFacts, 0, len(step.CallKeys))
		for _, key := range step.CallKeys {
			call := byKey[key]
			assert.Equal(t, runrecord.Adopted, call.Class)
			require.NotNil(t, call.Completion.Facts)
			facts = append(facts, *call.Completion.Facts)
		}
		state = compaction.Observe(state, facts)
		previous = step.ViewRef
	}
	assert.Greater(t, transitions, 2)
	assert.Equal(t, 4000, state.MinB)
	assert.False(t, state.Flags.BudgetTooSmall)
	assert.Equal(t, runrecord.Abandoned, byKey[recorder.Keys()[0]].Class)
	compactedEvents := 0
	for _, event := range rr.Events {
		if event.Type == hooks.ContextCompacted {
			compactedEvents++
			assert.Contains(t, string(event.Payload), `"call_keys":[]`)
		}
	}
	assert.Equal(t, transitions, compactedEvents)
	provider.limits.ContextWindow = 100000
	again, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
	require.NoError(t, err)
	assert.Equal(t, record, again, "adapter limits cannot influence recorded reconstruction")
}

func TestWorkflowCompactionEvents(t *testing.T) {
	for _, tc := range []struct {
		name string
		fact contextmeasure.RequestFacts
		kind hooks.EventType
	}{
		{"rewrite", contextmeasure.RequestFacts{Model: "m"}, hooks.ContextUnmeasured},
		{"unknown", contextmeasure.RequestFacts{Model: "m", ViewBearing: true}, hooks.ContextLimitsUnknown},
		{"tiny", contextmeasure.RequestFacts{Model: "m", ViewBearing: true, ContextWindow: 2899}, hooks.ContextBudgetTooSmall},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New()
			wf := &testWorkflowContext{ctx: t.Context(), hookRuntime: rt}
			base := &workflowPlanInput{compaction: &ContextCompaction{}}
			input := PlanActivityInput{AgentID: "agent", RunID: "run"}
			for step := 1; step <= 3; step++ {
				base.compactionDecision = compaction.Decision{State: base.compactionState}
				require.NoError(t, rt.adoptCompaction(wf, base, input, &PlanActivityOutput{Facts: []contextmeasure.RequestFacts{tc.fact}}, index.ViewRef{}))
			}
			page, err := rt.RunEventStore.List(t.Context(), "run", "", 10)
			require.NoError(t, err)
			require.Len(t, page.Events, 1)
			assert.Equal(t, tc.kind, page.Events[0].Type)
			assert.Equal(t, string(tc.kind)+":run", page.Events[0].EventKey)
		})
	}
	intercepted := 0
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{BeforeEventFunc: func(context.Context, *BeforeEventInput) (*BeforeEventDecision, error) {
		intercepted++
		return &BeforeEventDecision{Drop: true}, nil
	}}))
	wf := &testWorkflowContext{ctx: t.Context(), hookRuntime: rt}
	for _, kind := range []hooks.EventType{hooks.ContextCompacted, hooks.ContextCompactionSkipped} {
		event := hooks.NewContextCompactionEvent("run", "agent", "", 2, kind, "threshold", "evict", 1, 1, 9000, 1000, nil, wf.Now())
		require.NoError(t, rt.publishHook(wf.Context(), event, ""))
		require.NoError(t, rt.publishHook(wf.Context(), event, ""))
	}
	assert.Zero(t, intercepted)
	page, err := rt.RunEventStore.List(t.Context(), "run", "", 10)
	require.NoError(t, err)
	require.Len(t, page.Events, 2)
	for _, event := range page.Events {
		assert.Equal(t, fmt.Sprintf("%s:run:2", event.Type), event.EventKey)
		assert.Contains(t, string(event.Payload), `"call_keys":[]`)
	}
}

func TestWorkflowCompactionDecisions(t *testing.T) {
	rt := New()
	base := compactionFixture(t, rt.RunStore, 6)
	base.compaction = &ContextCompaction{KeepRecentTokens: 10, ProtectRecentToolTokens: 10, MinSavingTokens: 100}
	base.compactionState = compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{{Model: "m", ViewBearing: true, ContextWindow: 6000, FixedBytes: 90000}})
	input := PlanActivityInput{View: base.view(), Finalize: &planner.Termination{}}
	prepareCompaction(base, &input)
	assert.Equal(t, compaction.Evict, base.compactionDecision.Tier)
	assert.Positive(t, base.compactionDecision.State.NoProgressAt)
	loaded, err := rt.loadTestView(t.Context(), "root", "run", input.View)
	require.NoError(t, err)
	base.compactionState = base.compactionDecision.State
	base.compactionView = input.View
	base.compactionView.IndexHead, base.compactionView.Pending = loaded.Head, nil
	base.compactionView.Checkpoint = &loaded.View.Checkpoint
	input.View = base.view()
	prepareCompaction(base, &input)
	assert.Equal(t, compaction.NoProgress, base.compactionDecision.Skipped)
	// Changing only recorded size grows the estimate through the exact guard boundary.
	last := len(base.Entries) - 1
	base.Entries[last].Bytes += 297
	input.View = base.view()
	prepareCompaction(base, &input)
	assert.Equal(t, compaction.NoProgress, base.compactionDecision.Skipped)
	base.Entries[last].Bytes += 3
	input.View = base.view()
	prepareCompaction(base, &input)
	assert.Greater(t, base.compactionDecision.State.NoProgressAt, base.compactionState.NoProgressAt, "growth reaches a new compaction attempt")
	// Sticky cutoff wins even after a later larger model budget.
	base.compactionState = compaction.Observe(base.compactionState, []contextmeasure.RequestFacts{{Model: "m", ViewBearing: true, ContextWindow: 2899}, {Model: "m", ViewBearing: true, ContextWindow: 6000}})
	input.View = base.view()
	prepareCompaction(base, &input)
	assert.Equal(t, input.View, base.view())
	assert.Equal(t, compaction.None, base.compactionDecision.Tier)
}

func TestChildCompactionSnapshot(t *testing.T) {
	rt := New(WithContextCompaction("child", ContextCompaction{KeepResultTools: []tools.Ident{"keep"}}))
	input := RunInput{AgentID: "parent"}
	rt.stampContextCompaction(&input)
	native := &testWorkflowContext{ctx: t.Context()}
	native.ctx = engine.WithWorkflowContext(native.ctx, native)
	wrapper := compactionWorkflowContext{WorkflowContext: native, settings: input.CompactionAgents}
	assert.Same(t, native, engine.WorkflowContextFromContext(wrapper.Context()), "preserve native await identity")
	child := RunInput{AgentID: "child"}
	inheritCompaction(wrapper.Context(), &child)
	require.NotNil(t, child.ContextCompaction)
	assert.Equal(t, contextmeasure.Rate{Tokens: 85, Bytes: 100}, child.ContextCompaction.TriggerAt)
	child.ContextCompaction.KeepResultTools[0] = "changed"
	assert.Equal(t, []tools.Ident{"keep"}, input.CompactionAgents["child"].KeepResultTools)
}

func TestWorkflowTierOne(t *testing.T) {
	for _, saving := range []int{100, 20000} {
		t.Run(fmt.Sprint(saving), func(t *testing.T) {
			rt := New()
			base := &workflowPlanInput{AgentID: "agent", RunContext: run.Context{RootRunID: "root", RunID: "run"}, protocolVersion: api.WorkflowProtocolVersion, compaction: &ContextCompaction{MinSavingTokens: saving, ProtectRecentToolTokens: 1, KeepRecentTokens: 100000}}
			require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Message, Role: model.ConversationRoleUser, Bytes: 4, Hash: compactionPut(t, rt.RunStore, &model.Message{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "user"}}})}))
			for i := 0; i < 3; i++ {
				id := fmt.Sprintf("call-%d", i)
				call := compactionPut(t, rt.RunStore, planner.ToolRequest{Name: "test.lookup", ToolCallID: id})
				require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Assistant, Calls: []runstore.Hash{call}, ToolUseIDs: []string{id}, Role: model.ConversationRoleAssistant, Bytes: 20}))
				text := strings.Repeat("x", 18000)
				hash := compactionPut(t, rt.RunStore, text)
				require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.ToolResult, Hash: hash, ToolUseIDs: []string{id}, Role: model.ConversationRoleUser, Bytes: len(text) + 2, Keep: i == 0, Batch: index.MessageID(len(base.Entries) + 1), Result: &index.ResultProjection{ContentBytes: len(text) + 2}}))
			}
			base.compactionState = compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{{Model: "m", ViewBearing: true, ContextWindow: 20000}})
			input := PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}
			prepareCompaction(base, &input)
			if saving == 100 {
				assert.Equal(t, compaction.Clear, base.compactionDecision.Tier)
				assert.Equal(t, index.MessageID(5), input.View.ClearedThrough)
				loaded, err := rt.loadTestView(t.Context(), "root", "run", input.View)
				require.NoError(t, err)
				text := loaded.Messages[2].Parts[0].(model.ToolResultPart).Content.(string)
				assert.Len(t, text, 18000, "Keep remains whole")
				cleared := loaded.Messages[4].Parts[0].(model.ToolResultPart).Content
				assert.Equal(t, checkpoint.ClearedResult(18002, "call-1", ""), cleared)
			} else {
				assert.Zero(t, input.View.ClearedThrough)
			}
		})
	}
}

func TestCompactionAdoptionSurvivesDiagnosticFailure(t *testing.T) {
	failure := errors.New("diagnostic hook failed")
	rt := New(WithInterceptors(RuntimeInterceptorFuncs{BeforeEventFunc: func(context.Context, *BeforeEventInput) (*BeforeEventDecision, error) { return nil, failure }}))
	base := compactionFixture(t, rt.RunStore, 3)
	base.compaction = &ContextCompaction{}
	view := base.view()
	view.TailFrom, view.Budget = 4, compactionBudget()
	loaded, err := rt.loadTestView(t.Context(), "root", "run", view)
	require.NoError(t, err)
	key := runstore.CallKey{RunID: "run", Seq: 1, Attempt: 1}
	out := &PlanActivityOutput{IndexHead: loaded.Head, View: loaded.View, Facts: []contextmeasure.RequestFacts{{Call: key, Model: "m"}}, CallKeys: []runstore.CallKey{key}}
	wf := &testWorkflowContext{ctx: t.Context(), hookRuntime: rt}
	err = rt.commitPlanActivityOutput(wf, base, PlanActivityInput{AgentID: "agent", RunID: "run", View: view, Step: 1}, out)
	require.ErrorIs(t, err, failure)
	assert.Equal(t, loaded.Head, base.IndexHead)
	require.NotNil(t, base.compactionView.Checkpoint)
	assert.Equal(t, loaded.View.Checkpoint, *base.compactionView.Checkpoint)
	assert.True(t, base.compactionState.Flags.Unmeasured)
	page, err := rt.RunEventStore.List(t.Context(), "run", "", 10)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	assert.Equal(t, hooks.PlannerStep, page.Events[0].Type)
}

func TestWorkflowCompactionDiagnosticsFromPlanner(t *testing.T) {
	for _, tc := range []struct {
		name    string
		window  int
		reserve int
		rewrite bool
		kind    hooks.EventType
	}{
		{"rewriting planner", 6000, 0, true, hooks.ContextUnmeasured},
		{"unknown limits", 0, 0, false, hooks.ContextLimitsUnknown},
		{"small budget", 2899, 0, false, hooks.ContextBudgetTooSmall},
		{"exhausted reserve", 6000, 6000, false, hooks.ContextBudgetTooSmall},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := New()
			base := compactionFixture(t, rt.RunStore, 3)
			base.compaction = &ContextCompaction{}
			provider := &recordingFactsClient{recordingTestClient: &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{Content: []model.Message{{Role: model.ConversationRoleAssistant, Parts: []model.Part{model.TextPart{Text: "done"}}}}, StopReason: "end_turn"}}, limits: model.ContextLimits{ContextWindow: tc.window}, resolved: "m"}
			require.NoError(t, rt.RegisterModel("fake", provider))
			rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{start: func(ctx context.Context, input *planner.PlanInput) (*planner.PlanResult, error) {
				messages := input.Messages
				if tc.rewrite {
					messages = []*model.Message{{Role: model.ConversationRoleUser, Parts: []model.Part{model.TextPart{Text: "planner-owned replacement"}}}}
				}
				client, ok := input.Agent.ModelClient("fake")
				require.True(t, ok)
				_, err := client.Complete(ctx, &model.Request{Model: "fake", Messages: messages, MaxTokens: tc.reserve})
				if err != nil {
					return nil, err
				}
				return assistantCommitResult(), nil
			}}}
			wf := &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}
			for i := 0; i < 3; i++ {
				out, err := rt.runPlanActivity(wf, base, "plan", engine.ActivityOptions{}, PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}, time.Time{})
				require.NoError(t, err)
				require.Len(t, out.Facts, 1)
				assert.Equal(t, !tc.rewrite, out.Facts[0].ViewBearing)
				assert.Zero(t, base.compactionView.TailFrom)
			}
			page, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
			require.NoError(t, err)
			count := 0
			for _, event := range page.Events {
				if event.Type == tc.kind {
					count++
					assert.Equal(t, string(tc.kind)+":run", event.EventKey)
				}
			}
			assert.Equal(t, 1, count)
		})
	}
}
