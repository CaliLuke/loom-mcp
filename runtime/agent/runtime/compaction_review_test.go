package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type reviewRecoveryClient struct {
	*recordingFactsClient
	calls    int
	rejected error
}

func TestWorkflowCompactionRecoveryReconstruction(t *testing.T) {
	rt := New()
	base := compactionFixture(t, rt.RunStore, 30)
	base.compaction = &ContextCompaction{TriggerAt: contextmeasure.Rate{Tokens: 1, Bytes: 100}, KeepRecentTokens: 30, MinSavingTokens: 20}
	provider := &reviewRecoveryClient{recordingFactsClient: &recordingFactsClient{recordingTestClient: &recordingTestClient{response: &model.Response{Content: []model.Message{{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "ok"}}}}, StopReason: "end_turn"}, store: &recordingTestStore{Store: rt.RunStore}}, limits: model.ContextLimits{ContextWindow: 6000}, resolved: "measured"}, rejected: restoredValidationError(t, model.OutputValidationToolArguments)}
	require.NoError(t, rt.RegisterModel("fake", provider))
	rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
		client, ok := in.Agent.ModelClient("fake")
		require.True(t, ok)
		_, err := client.Complete(ctx, &model.Request{Model: "fake", Messages: in.Messages})
		if err != nil {
			return nil, err
		}
		return assistantCommitResult(), nil
	}}}
	wf := &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}
	var correction *ModelRecovery
	for step := 0; step < 4; step++ {
		input := PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}
		if step == 2 {
			input.Recovery = correction
		}
		out, err := rt.runPlanActivity(wf, base, "plan", engine.ActivityOptions{}, input, time.Time{})
		require.NoError(t, err)
		if step == 1 {
			require.NotNil(t, out.Recovery)
			assert.Nil(t, out.View)
			assert.Nil(t, base.view().Checkpoint, "a rejected loader view is not adopted")
			correction = out.Recovery
		}
	}
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
	require.NoError(t, err)
	rr := record.Runs[0]
	source := func(_ context.Context, hash runstore.Hash) ([]byte, error) { return record.Content[hash], nil }
	byKey := map[runstore.CallKey]runrecord.Call{}
	for _, call := range rr.Calls {
		byKey[call.Record.Key] = call
	}
	var state compaction.State
	var stable index.ViewRef
	count := 0
	for _, event := range rr.Events {
		if event.Type != hooks.PlannerStep {
			continue
		}
		var step hooks.PlannerStepEvent
		require.NoError(t, json.Unmarshal(event.Payload, &step))
		assert.Equal(t, step.Step == 2, step.Recovery)
		assert.Equal(t, step.Step == 3, step.Recovering)
		entries, err := index.Walk(t.Context(), source, step.IndexHead)
		require.NoError(t, err)
		stable.IndexHead = step.IndexHead
		decision := compaction.Decide(state, entries, stable, *rr.CompactionSettings)
		if step.Recovering {
			decision.View, decision.State = stable, state
		}
		assert.Equal(t, decision.View.TailFrom, step.ViewRef.TailFrom)
		assert.Equal(t, decision.View.ClearedThrough, step.ViewRef.ClearedThrough)
		assert.Equal(t, decision.View.Budget, step.ViewRef.Budget)
		pins := stable.Pinned
		expected := decision.View
		if step.ViewRef.Checkpoint != nil {
			var previous index.CheckpointRecord
			if stable.Checkpoint != nil {
				require.NoError(t, json.Unmarshal(record.Content[*stable.Checkpoint], &previous))
			}
			if decision.View.TailFrom > previous.Through+1 {
				lines, err := receiptLines(t.Context(), source, entries, previous.Through, decision.View.TailFrom)
				require.NoError(t, err)
				candidate := index.CheckpointRecord{Prev: stable.Checkpoint, Summary: previous.Summary, SummaryRun: previous.SummaryRun, CitedUsers: previous.CitedUsers, Receipt: lines, Through: decision.View.TailFrom - 1, Chain: entries[decision.View.TailFrom-2].Chain}
				data, err := json.Marshal(candidate, json.Deterministic(true))
				require.NoError(t, err)
				hash := runstore.Hash(fmt.Sprintf("sha256:%x", sha256.Sum256(data)))
				expected.Checkpoint = &hash
			}
			input, err := checkpoint.FromRecord(t.Context(), source, *step.ViewRef.Checkpoint, entries, 0)
			require.NoError(t, err)
			if decision.View.TailFrom != stable.TailFrom || decision.View.Budget != stable.Budget {
				pins = checkpoint.SelectPins(input.Users, decision.View.Budget, checkpoint.DefaultLimits)
			}
		}
		assert.Equal(t, pins, step.ViewRef.Pinned, "pins are part of the reconstructed transition")
		expected.Pinned = pins
		assert.Equal(t, expected, step.ViewRef, "the complete ViewRef transition reproduces, including checkpoint and pins")
		if !step.Recovery {
			state, stable = decision.State, step.ViewRef
			if stable.Checkpoint != nil {
				state.Rendered = compaction.RenderedCheckpoint{Hash: string(*stable.Checkpoint), TailFrom: stable.TailFrom, Budget: stable.Budget, Bytes: step.CheckpointBytes}
			}
		}
		facts := make([]contextmeasure.RequestFacts, 0, len(step.CallKeys))
		for _, key := range step.CallKeys {
			call := byKey[key]
			assert.Equal(t, step.ViewRef, *call.Started.Request.View)
			require.NotNil(t, call.Completion.Facts)
			facts = append(facts, *call.Completion.Facts)
		}
		state = compaction.Observe(state, facts)
		count++
	}
	assert.Equal(t, 4, count)
}

func TestCompactionSkippedEpisodes(t *testing.T) {
	rt := New()
	base := &workflowPlanInput{compaction: &ContextCompaction{MinSavingTokens: 100}, compactionState: compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{{Model: "m", ViewBearing: true, ContextWindow: 6000, FixedBytes: 90000}})}
	wf := &testWorkflowContext{ctx: t.Context(), hookRuntime: rt}
	for step := 1; step <= 8; step++ {
		if step == 4 {
			base.compactionState.Models["m"] = compaction.ModelState{Rate: contextmeasure.Rate{Tokens: 1, Bytes: 3}, Known: true, Budget: 6000, FixedBytes: 0}
		}
		if step == 5 {
			base.compactionState.Models["m"] = compaction.ModelState{Rate: contextmeasure.Rate{Tokens: 1, Bytes: 3}, Known: true, Budget: 6000, FixedBytes: 90000}
		}
		if step == 7 {
			model := base.compactionState.Models["m"]
			model.FixedBytes += 300
			base.compactionState.Models["m"] = model
		}
		input := PlanActivityInput{RunID: "run", Step: step, View: base.view()}
		if step == 3 {
			input.Recovery = &ModelRecovery{}
		}
		prepareCompaction(base, &input)
		out := &PlanActivityOutput{}
		require.NoError(t, rt.commitPlanActivityOutput(wf, base, input, out))
	}
	page, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
	require.NoError(t, err)
	var skipped []string
	for _, event := range page.Events {
		if event.Type == hooks.ContextCompactionSkipped {
			skipped = append(skipped, event.EventKey)
		}
	}
	assert.Equal(t, []string{"context_compaction_skipped:run:1", "context_compaction_skipped:run:5", "context_compaction_skipped:run:7"}, skipped)
}

func (c *reviewRecoveryClient) Complete(ctx context.Context, request *model.Request) (*model.Response, error) {
	c.calls++
	if c.calls == 2 {
		return nil, c.rejected
	}
	return c.recordingFactsClient.Complete(ctx, request)
}

func TestWorkflowCompactionHistoryPolicyConflict(t *testing.T) {
	client := New(WithContextCompaction("agent", ContextCompaction{}))
	input := &RunInput{AgentID: "agent", RootRunID: "run", RunID: "run", WorkflowProtocolVersion: api.WorkflowProtocolVersion}
	client.stampContextCompaction(input)
	before := 0
	worker := New(WithInterceptors(RuntimeInterceptorFuncs{BeforeRunFunc: func(context.Context, *BeforeRunInput) (*BeforeRunDecision, error) {
		before++
		return nil, nil
	}}))
	require.NoError(t, worker.RegisterAgent(t.Context(), AgentRegistration{ID: "agent", Planner: &stubPlanner{}, Workflow: engine.WorkflowDefinition{Name: "workflow", TaskQueue: "queue", Handler: worker.ExecuteWorkflow}, PlanActivityName: "plan", ResumeActivityName: "resume", ExecuteToolActivity: "tool", Policy: RunPolicy{History: func(context.Context, []*model.Message, []*model.ToolDefinition) ([]*model.Message, error) {
		return nil, nil
	}}}))
	wf := &testWorkflowContext{ctx: t.Context(), runtime: worker}
	_, err := worker.ExecuteWorkflow(wf, input)
	require.ErrorContains(t, err, "HistoryPolicy")
	assert.Zero(t, before)
	assert.Empty(t, wf.plannerCalls)
	page, err := worker.RunEventStore.List(t.Context(), "run", "", 10)
	require.NoError(t, err)
	assert.Empty(t, page.Events)
}

func TestCompactionOptOut(t *testing.T) {
	rt := New()
	base := &workflowPlanInput{}
	wf := &testWorkflowContext{ctx: t.Context(), hookRuntime: rt}
	input := PlanActivityInput{RunID: "run"}
	for _, fact := range []contextmeasure.RequestFacts{{Model: "rewritten"}, {Model: "unknown", ViewBearing: true}, {Model: "tiny", ViewBearing: true, ContextWindow: 2000}} {
		require.NoError(t, rt.adoptCompaction(wf, base, input, &PlanActivityOutput{Facts: []contextmeasure.RequestFacts{fact}}, index.ViewRef{}))
		prepareCompaction(base, &input)
		assert.Empty(t, input.Models)
		assert.Equal(t, compaction.State{}, base.compactionState)
	}
	page, err := rt.RunEventStore.List(t.Context(), "run", "", 10)
	require.NoError(t, err)
	assert.Empty(t, page.Events)
	for _, test := range []struct {
		name    string
		window  int
		rewrite bool
	}{
		{"rewritten", 6000, true}, {"unknown", 0, false}, {"tiny", 2000, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			rt := New()
			base := compactionFixture(t, rt.RunStore, 3)
			provider := &recordingFactsClient{recordingTestClient: &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, response: &model.Response{Content: []model.Message{{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "answer"}}}}, StopReason: "end_turn"}}, limits: model.ContextLimits{ContextWindow: test.window}, resolved: "m"}
			require.NoError(t, rt.RegisterModel("fake", provider))
			rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
				messages := in.Messages
				if test.rewrite {
					messages = []*model.Message{contentMessage("replacement")}
				}
				client, ok := in.Agent.ModelClient("fake")
				require.True(t, ok)
				_, err := client.Complete(ctx, &model.Request{Model: "fake", Messages: messages})
				if err != nil {
					return nil, err
				}
				return assistantCommitResult(), nil
			}}}
			wf := &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}
			for step := 0; step < 3; step++ {
				_, err := rt.runPlanActivity(wf, base, "plan", engine.ActivityOptions{}, PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}, time.Time{})
				require.NoError(t, err)
				assert.Equal(t, compaction.State{}, base.compactionState)
				assert.Nil(t, wf.plannerCalls[step].Input.Models)
				assert.Equal(t, index.RenderBudget{}, wf.plannerCalls[step].Input.View.Budget)
			}
			page, err := rt.RunEventStore.List(t.Context(), "run", "", 100)
			require.NoError(t, err)
			types := make([]hooks.EventType, 0, len(page.Events))
			for _, event := range page.Events {
				types = append(types, event.Type)
			}
			want := []hooks.EventType{hooks.AssistantMessage, hooks.PlannerStep, hooks.AssistantMessage, hooks.PlannerStep, hooks.AssistantMessage, hooks.PlannerStep}
			if test.rewrite {
				want = []hooks.EventType{hooks.AssistantMessage, hooks.PlannerStep, hooks.ContextRequestGrowth, hooks.AssistantMessage, hooks.PlannerStep, hooks.AssistantMessage, hooks.PlannerStep}
			}
			assert.Equal(t, want, types, "opt-out preserves pre-K12 events")
		})
	}
}

func TestSignalMeasurementRequired(t *testing.T) {
	for _, sizes := range [][]contextmeasure.MessageSize{nil, {{}, {}}} {
		base := &workflowPlanInput{}
		attempt := 1
		err := applyResumeMessages(base, &attempt, &api.ResumeRequest{MessageRefs: []runstore.Hash{"message"}, MessageSizes: sizes})
		require.ErrorContains(t, err, "measurement")
		assert.Empty(t, base.Entries)
		assert.Equal(t, 1, attempt)
	}
	base := &workflowPlanInput{}
	require.ErrorContains(t, appendClarificationAnswer(base, "answer", nil), "measurement")
	assert.Empty(t, base.Entries)
	require.NoError(t, appendClarificationAnswer(base, "empty-answer", new(0)))
}

func TestReminderEstimateAfterEviction(t *testing.T) {
	rt := New()
	base := compactionFixture(t, rt.RunStore, 3)
	state := compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{{Model: "m", ViewBearing: true, ContextWindow: 6000}})
	settings := compaction.Settings{TriggerAt: contextmeasure.Rate{Tokens: 1, Bytes: 100}, KeepRecentTokens: 10, MinSavingTokens: 10000}
	view := base.view()
	for step := 0; step < 15; step++ {
		require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Structural, Structural: &index.StructuralEntry{Kind: index.Reminders, ProtocolVersion: index.TemplateProtocolVersion, Text: []index.TextParameter{{Text: strings.Repeat("reminder ", 200)}}}}))
		message := &model.Message{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "next"}}}
		require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Message, Role: "assistant", Bytes: 4, Hash: compactionPut(t, rt.RunStore, message)}))
		view.IndexHead, view.Pending = base.IndexHead, base.Entries[base.Committed:]
		decision := compaction.Decide(state, base.Entries, view, settings)
		loaded, err := rt.loadView(t.Context(), "root", "run", decision.View)
		require.NoError(t, err)
		require.NotNil(t, loaded.View)
		view = decision.View
		view.IndexHead, view.Pending, view.Checkpoint, view.Pinned = loaded.Head, nil, &loaded.View.Checkpoint, loaded.View.Pinned
		base.adoptHead(loaded.Head)
		state.Rendered = compaction.RenderedCheckpoint{Hash: string(*view.Checkpoint), TailFrom: view.TailFrom, Budget: view.Budget, Bytes: loaded.View.CheckpointBytes}
		size, err := contextmeasure.MeasureMessages(loaded.Messages)
		require.NoError(t, err)
		next := compaction.Decide(state, base.Entries, view, settings)
		assert.GreaterOrEqual(t, next.Before, contextmeasure.Estimate(state.Models["m"].Rate, 0, size.Bytes, size.NonText), "step %d includes every resident reminder", step)
	}
}

func TestCanonicalPinsEquivalent(t *testing.T) {
	rt := New()
	base := compactionFixture(t, rt.RunStore, 3)
	user := contentMessage(strings.Repeat("newest ", 40))
	size, err := contextmeasure.MeasureMessages([]*model.Message{user})
	require.NoError(t, err)
	require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Message, Role: "user", Hash: compactionPut(t, rt.RunStore, user), Bytes: size.Bytes}))
	assistant := &model.Message{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "tail"}}}
	require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Message, Role: "assistant", Hash: compactionPut(t, rt.RunStore, assistant), Bytes: 4}))
	view := base.view()
	view.TailFrom, view.Budget = 9, compactionBudget()
	loaded, err := rt.loadView(t.Context(), "root", "run", view)
	require.NoError(t, err)
	require.NotEmpty(t, loaded.View.Pinned)
	view.IndexHead, view.Pending, view.Checkpoint = loaded.Head, nil, &loaded.View.Checkpoint
	view.Pinned = loaded.View.Pinned
	assert.Equal(t, []index.MessageID{1, 8}, view.Pinned)
	for _, budget := range []int{400, 40} {
		view.Budget.KeepUserTokens = budget
		loaded, err := rt.loadView(t.Context(), "root", "run", view)
		require.NoError(t, err)
		in, err := checkpoint.FromRecord(t.Context(), func(ctx context.Context, h runstore.Hash) ([]byte, error) {
			return rt.RunStore.Get(ctx, "root", h)
		}, *view.Checkpoint, base.Entries, 0)
		require.NoError(t, err)
		assert.Equal(t, checkpoint.SelectPins(in.Users, view.Budget, checkpoint.DefaultLimits), loaded.View.Pinned)
		view.Pinned = loaded.View.Pinned
		for _, runtime := range []*Runtime{rt, New(WithRunStore(rt.RunStore))} {
			loaded, err := runtime.loadView(t.Context(), "root", "run", view)
			require.NoError(t, err)
			assert.Equal(t, view.Pinned, loaded.View.Pinned)
			want, err := checkpoint.Project(t.Context(), func(ctx context.Context, h runstore.Hash) ([]byte, error) {
				return rt.RunStore.Get(ctx, "root", h)
			}, base.Entries, view)
			require.NoError(t, err)
			assert.Equal(t, want, loaded.Messages)
		}
	}
	assert.Equal(t, []index.MessageID{1}, view.Pinned, "shrinking the budget recomputes pins")
}

func TestMessageToolReceiptCoverage(t *testing.T) {
	for _, resumed := range []bool{false, true} {
		t.Run(map[bool]string{false: "seed", true: "resume"}[resumed], func(t *testing.T) {
			rt := New()
			messages := []*model.Message{
				{Role: "user", Parts: []model.Part{model.TextPart{Text: "find it"}}},
				{Role: "assistant", Parts: []model.Part{model.ToolUsePart{ID: "search-1", Name: "search", Input: rawjson.Message(`{"query":"` + strings.Repeat("é", 200) + `"}`)}}},
				{Role: "user", Parts: []model.Part{model.ToolResultPart{ToolUseID: "search-1", Content: "found"}}},
				{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "ok"}}},
				{Role: "user", Parts: []model.Part{model.TextPart{Text: "next"}}},
				{Role: "assistant", Parts: []model.Part{model.TextPart{Text: "tail"}}},
			}
			base := &workflowPlanInput{}
			if resumed {
				request := &api.ResumeRequest{}
				for _, message := range messages {
					request.MessageRefs = append(request.MessageRefs, compactionPut(t, rt.RunStore, message))
					size, err := contextmeasure.MeasureMessages([]*model.Message{message})
					require.NoError(t, err)
					request.MessageSizes = append(request.MessageSizes, size)
				}
				attempt := 1
				require.NoError(t, applyResumeMessages(base, &attempt, request))
			} else {
				items, err := encodeContentMessages(messages)
				require.NoError(t, err)
				out, err := rt.recordContentItems(t.Context(), &api.RecordContentInput{RootRunID: "root", Items: items, SeedMessages: true})
				require.NoError(t, err)
				base.Entries, base.IndexHead, base.Committed = out.Entries, out.IndexHead, len(out.Entries)
			}
			view := base.view()
			view.TailFrom, view.Budget = 6, compactionBudget()
			loaded, err := rt.loadView(t.Context(), "root", "run", view)
			require.NoError(t, err)
			data, err := rt.RunStore.Get(t.Context(), "root", loaded.View.Checkpoint)
			require.NoError(t, err)
			var record index.CheckpointRecord
			require.NoError(t, json.Unmarshal(data, &record))
			require.Len(t, record.Receipt, 2)
			assert.Equal(t, "search", record.Receipt[0].Tool)
			assert.Equal(t, "search-1", record.Receipt[0].ToolUseID)
			assert.LessOrEqual(t, len(record.Receipt[0].Text), checkpoint.ReceiptArgsBytes)
			assert.Equal(t, "ok", record.Receipt[1].Text)
			input, err := checkpoint.FromRecord(t.Context(), func(ctx context.Context, h runstore.Hash) ([]byte, error) {
				return rt.RunStore.Get(ctx, "root", h)
			}, loaded.View.Checkpoint, base.Entries, 0)
			require.NoError(t, err)
			for _, user := range input.Users {
				assert.NotEqual(t, index.MessageID(3), user.ID, "tool result is covered by the call receipt")
			}
			assert.NotContains(t, loaded.Messages[0].Parts[0].(model.TextPart).Text, "[m3]")
		})
	}
}
