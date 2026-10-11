package runtime

import (
	"context"
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
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/overflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type summaryWorkflowContext struct {
	*testWorkflowContext
	calls   int
	stale   bool
	advance time.Duration
	now     time.Time
}

func TestWorkflowSummaryAdoption(t *testing.T) {
	for _, kind := range []string{"", "empty", "truncated", "too_large", "not_smaller", "cites_unknown", "cites_not_behind", "cites_non_user", "missing_headings", "names_internal_tool", "summariser_limits_unknown", "summariser_error", "summariser_overflow", "stale"} {
		t.Run(kind, func(t *testing.T) {
			rt, in, client, entries := summaryFixture(t)
			switch kind {
			case "summariser_limits_unknown":
				client.limits = model.ContextLimits{}
			case "summariser_error":
				client.errors = []error{context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded}
			case "summariser_overflow":
				client.errors = []error{model.NewProviderError("summary", "complete", 400, model.ProviderErrorKindContextOverflow, "context_length_exceeded", "overflow", "", false, nil)}
			case "empty":
				client.response = summaryResponse("")
			case "truncated":
				client.response.OutputLimited = true
			case "too_large":
				client.response = summaryResponse(strings.Repeat("x", 3000))
			case "not_smaller":
				client.response = summaryResponse(strings.Repeat("x", 1900))
				in.ViewRef.ClearedThrough = 3
			case "cites_not_behind":
				client.response = summaryResponse(strings.Replace(summaryTestText, "[message 1]", "[message 5]", 1))
			case "cites_non_user":
				client.response = summaryResponse(strings.Replace(summaryTestText, "[message 1]", "[message 2]", 1))
			case "missing_headings":
				client.response = summaryResponse("short fact")
			case "names_internal_tool":
				client.response = summaryResponse(summaryTestText + " recall_context")
			case "cites_unknown":
				client.response = summaryResponse(summaryTestText + " [r2 message 1]")
			}
			base := summaryWorkflowBase(in, entries)
			wf := &summaryWorkflowContext{testWorkflowContext: &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}, stale: kind == "stale"}
			input := PlanActivityInput{AgentID: in.AgentID, RunID: in.RunID, RunContext: base.RunContext, View: base.compactionDecision.View, Step: 9, ContextSettings: base.contextSettings}
			original := base.view()
			require.NoError(t, rt.prepareWorkflowSummary(wf, base, &input, time.Time{}))
			assert.Equal(t, 1, wf.calls)
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, in.Scope, in.RunID)
			require.NoError(t, err)
			wantCalls := 1
			if kind == "summariser_limits_unknown" {
				wantCalls = 0
			} else if kind == "summariser_error" {
				wantCalls = 3
			}
			require.Len(t, record.Runs[0].Calls, wantCalls)
			for _, call := range record.Runs[0].Calls {
				assert.Equal(t, runrecord.Adopted, call.Class)
			}
			var event hooks.ContextCompactionEvent
			require.NoError(t, json.Unmarshal(record.Runs[0].Events[0].Payload, &event))
			assert.Equal(t, kind, event.Kind)
			switch kind {
			case "":
				require.NotNil(t, input.View.Checkpoint)
				assert.Equal(t, in.To, input.View.TailFrom)
				assert.Equal(t, "summary", event.Tier)
				assert.Equal(t, input.View.Checkpoint, event.Checkpoint)
				assert.Equal(t, 1, event.Chunks)
				require.NotNil(t, record.Runs[0].ContextSettings)
				assert.Equal(t, "summary", record.Runs[0].ContextSettings.Summary.Model.ID)
			case "stale":
				assert.Equal(t, original, base.view())
				assert.Equal(t, original, input.View)
				assert.Zero(t, base.compactionState.Summary.Rejections)
			default:
				assert.Nil(t, base.view().Checkpoint)
				loaded, err := rt.loadTestView(t.Context(), in.Scope, in.RunID, input.View)
				require.NoError(t, err)
				require.NotNil(t, loaded.View)
				data, err := rt.RunStore.Get(t.Context(), in.Scope, loaded.View.Checkpoint)
				require.NoError(t, err)
				var cp index.CheckpointRecord
				require.NoError(t, json.Unmarshal(data, &cp))
				assert.Equal(t, in.To-1, cp.Through)
				assert.Nil(t, cp.Summary)
			}
		})
	}
}

func TestWorkflowSummaryDisableAndRecovery(t *testing.T) {
	rt, in, client, entries := summaryFixture(t)
	client.response = summaryResponse("")
	base := summaryWorkflowBase(in, entries)
	wf := &summaryWorkflowContext{testWorkflowContext: &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}}
	for step := 1; step <= 4; step++ {
		input := PlanActivityInput{AgentID: in.AgentID, RunID: in.RunID, RunContext: base.RunContext, View: base.compactionDecision.View, Step: step, ContextSettings: base.contextSettings}
		require.NoError(t, rt.prepareWorkflowSummary(wf, base, &input, time.Time{}))
	}
	assert.Equal(t, 3, wf.calls)
	assert.True(t, base.compactionState.Summary.Disabled)
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, in.Scope, in.RunID)
	require.NoError(t, err)
	assert.Equal(t, base.compactionState.Summary, record.Runs[0].Summary)
	disabled := 0
	for _, event := range record.Runs[0].Events {
		var e hooks.ContextCompactionEvent
		require.NoError(t, json.Unmarshal(event.Payload, &e))
		if e.Reason == "summaries_disabled" {
			disabled++
		}
	}
	assert.Equal(t, 1, disabled)
	for _, mode := range []string{"overflow", "correction", "no model", "expired", "finalizer"} {
		t.Run(mode, func(t *testing.T) {
			fresh := summaryWorkflowBase(in, entries)
			input := PlanActivityInput{AgentID: in.AgentID, RunID: in.RunID, RunContext: fresh.RunContext, View: fresh.compactionDecision.View, Step: 10}
			deadline := time.Time{}
			switch mode {
			case "finalizer":
				input.Finalize = &planner.Termination{}
			case "overflow":
				input.Overflow = &overflow.Step{}
			case "correction":
				input.Recovery = &ModelRecovery{}
			case "no model":
				fresh.compaction.ContextModel = nil
			case "expired":
				deadline = wf.Now().Add(-time.Second)
			}
			before := wf.calls
			err := rt.prepareWorkflowSummary(wf, fresh, &input, deadline)
			if mode == "expired" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, before, wf.calls)
		})
	}
}

func TestSummaryContextModelStampAndSeal(t *testing.T) {
	config := ContextCompaction{ContextModel: &ContextModel{ID: "missing", Class: model.ModelClassSmall, AcceptsImages: true}, Summary: SummarySettings{Instructions: "keep facts"}}
	rt := New(WithContextCompaction("agent", config))
	config.ContextModel.ID = "changed"
	input := RunInput{AgentID: "agent"}
	rt.stampContextCompaction(&input)
	require.NotNil(t, input.ContextCompaction.ContextModel)
	assert.Equal(t, "missing", input.ContextCompaction.ContextModel.ID)
	input.ContextCompaction.ContextModel.ID = "mutated"
	assert.Equal(t, "missing", input.CompactionAgents["agent"].ContextModel.ID)
	require.ErrorContains(t, rt.Seal(t.Context()), "missing")
	valid := New(WithContextCompaction("agent", ContextCompaction{ContextModel: &ContextModel{ID: "summary"}}))
	require.NoError(t, valid.RegisterModel("summary", &summaryTestClient{}))
	require.NoError(t, valid.Seal(t.Context()))
}

func summaryWorkflowBase(in *SummarizeContextInput, entries []index.MessageEntry) *workflowPlanInput {
	settings := &ContextCompaction{ContextModel: &ContextModel{ID: in.ContextModel.ID}, Summary: in.Settings}
	base := &workflowPlanInput{AgentID: in.AgentID, RunContext: run.Context{RunID: in.RunID, RootRunID: in.Scope, Attempt: 1}, Entries: entries, Committed: len(entries), IndexHead: in.ViewRef.IndexHead, compaction: settings, compactionView: in.ViewRef, protocolVersion: api.WorkflowProtocolVersion}
	base.contextSettings = resolvedContextSettings(settings, ContextRecovery{})
	base.compactionState = compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{{ViewBearing: true, Model: "planner", ContextWindow: 200000}})
	view := base.view()
	view.TailFrom = in.To
	view.Budget = in.Budget
	base.compactionDecision = compaction.Decision{View: view, State: base.compactionState, Tier: compaction.Evict, Before: 10000, After: 1000}
	return base
}

func (w *summaryWorkflowContext) ExecuteSummarizeContextActivity(ctx context.Context, call engine.SummarizeContextActivityCall) (*api.SummarizeContextOutput, error) {
	w.calls++
	out, err := w.runtime.SummarizeContextActivity(ctx, call.Input)
	w.now = w.Now().Add(w.advance)
	if out != nil && w.stale {
		out.From++
	}
	return out, err
}

func TestWorkflowSummaryBeforePlanner(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint(reject), func(t *testing.T) {
			rt := New()
			base := compactionFixture(t, rt.RunStore, 30)
			base.compaction = &ContextCompaction{TriggerAt: contextmeasure.Rate{Tokens: 1, Bytes: 100}, KeepRecentTokens: 30, MinSavingTokens: 20, ContextModel: &ContextModel{ID: "summary"}}
			base.contextSettings = resolvedContextSettings(base.compaction, ContextRecovery{})
			base.compactionState = compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{{Model: "planner", ViewBearing: true, ContextWindow: 6000}})
			client := &summaryTestClient{store: rt.RunStore, limits: model.ContextLimits{ContextWindow: 200000}, response: summaryResponse(summaryTestText)}
			if reject {
				client.response = summaryResponse("")
			}
			require.NoError(t, rt.RegisterModel("summary", client))
			require.NoError(t, rt.registerSummaryPrompt("agent", SummarySettings{}))
			plannerCalls := 0
			rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
				plannerCalls++
				require.NotEmpty(t, client.requests, "summary runs before planner")
				record, err := runrecord.Read(ctx, rt.RunStore, rt.RunEventStore, "root", "run")
				require.NoError(t, err)
				require.NotEmpty(t, record.Runs[0].Calls)
				assert.Equal(t, runrecord.Adopted, record.Runs[0].Calls[0].Class)
				return assistantCommitResult(), nil
			}}}
			wf := &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}
			out, err := rt.runPlanActivity(wf, base, "plan", engine.ActivityOptions{}, PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}, time.Time{})
			require.NoError(t, err)
			require.NotNil(t, out.View)
			assert.Equal(t, 1, plannerCalls)
			record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
			require.NoError(t, err)
			var compacted, skipped int
			for _, event := range record.Runs[0].Events {
				if event.Type == hooks.ContextCompacted {
					compacted++
				}
				if event.Type == hooks.ContextCompactionSkipped {
					skipped++
				}
			}
			assert.Equal(t, 1, compacted)
			if reject {
				assert.Equal(t, 1, skipped)
			} else {
				assert.Zero(t, skipped)
			}
		})
	}
}

type summaryLogRecorder struct {
	telemetry.NoopLogger
	off int
}

func (l *summaryLogRecorder) Info(_ context.Context, message string, _ ...any) {
	if message == "context summaries are off: no ContextModel" {
		l.off++
	}
}

func TestWorkflowSummaryOptOutLogsOnce(t *testing.T) {
	rt := New()
	log := &summaryLogRecorder{}
	rt.logger = log
	base := &workflowPlanInput{}
	wf := &summaryWorkflowContext{testWorkflowContext: &testWorkflowContext{ctx: t.Context(), runtime: rt}}
	for step := 1; step <= 3; step++ {
		require.NoError(t, rt.prepareWorkflowSummary(wf, base, &PlanActivityInput{RunID: "run", Step: step}, time.Time{}))
	}
	assert.Equal(t, 1, log.off)
	assert.Zero(t, wf.calls)
}

func TestWorkflowSummaryCannotPlanAfterBudgetExpiry(t *testing.T) {
	rt, in, _, entries := summaryFixture(t)
	base := summaryWorkflowBase(in, entries)
	base.compaction.TriggerAt = contextmeasure.Rate{Tokens: 1, Bytes: 100}
	base.compaction.KeepRecentTokens = 1
	base.compaction.ProtectRecentToolTokens = 100000
	base.compactionState = compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{{ViewBearing: true, Model: "planner", ContextWindow: 6000}})
	wf := &summaryWorkflowContext{testWorkflowContext: &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}, advance: 2 * time.Second}
	out, err := rt.runPlanActivity(wf, base, "plan", engine.ActivityOptions{}, PlanActivityInput{AgentID: in.AgentID, RunID: in.RunID, RunContext: base.RunContext, View: base.view()}, wf.Now().Add(time.Second))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, out)
	assert.Equal(t, 1, wf.calls)
	assert.Empty(t, wf.plannerCalls)
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, in.Scope, in.RunID)
	require.NoError(t, err)
	require.Len(t, record.Runs[0].Calls, 1)
	assert.Equal(t, runrecord.Adopted, record.Runs[0].Calls[0].Class)
}

func TestWorkflowRejectedSummaryFinalizesAfterBudgetExpiry(t *testing.T) {
	rt, in, client, entries := summaryFixture(t)
	client.response = summaryResponse("")
	base := summaryWorkflowBase(in, entries)
	base.compaction.TriggerAt = contextmeasure.Rate{Tokens: 1, Bytes: 100}
	base.compaction.KeepRecentTokens = 1
	base.compaction.ProtectRecentToolTokens = 100000
	base.compactionState = compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{{ViewBearing: true, Model: "planner", ContextWindow: 6000}})
	wf := &summaryWorkflowContext{testWorkflowContext: &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}, advance: 2 * time.Second}
	out, err := rt.runPlanActivity(wf, base, "plan", engine.ActivityOptions{}, PlanActivityInput{AgentID: in.AgentID, RunID: in.RunID, RunContext: base.RunContext, View: base.view()}, wf.Now().Add(time.Second))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, out)
	require.Equal(t, 1, wf.calls)
	require.Empty(t, wf.plannerCalls)
	reg := AgentRegistration{ID: in.AgentID, ResumeActivityName: "resume", Planner: &stubPlanner{resume: func(context.Context, *planner.PlanResumeInput) (*planner.PlanResult, error) {
		return assistantCommitResult(), nil
	}}}
	rt.agents[in.AgentID] = reg
	final, err := rt.finalizeWithPlanner(wf, reg, &RunInput{AgentID: in.AgentID, RunID: in.RunID, RootRunID: in.Scope}, base, nil, model.TokenUsage{}, policy.CapsState{}, 1, "", nil, planner.TerminationReasonTimeBudget, wf.Now().Add(10*time.Second))
	require.NoError(t, err)
	require.NotNil(t, final)
	assert.NotEmpty(t, final.FinalRef)
	assert.Equal(t, 1, wf.calls, "the finalizer must not summarize")
	require.Len(t, wf.plannerCalls, 1)
	assert.NotNil(t, wf.plannerCalls[0].Input.Finalize)
	assert.Equal(t, 2, wf.plannerCalls[0].Input.Step, "a published summary reserves its schedule identity")
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, in.Scope, in.RunID)
	require.NoError(t, err)
	require.Len(t, record.Runs[0].Calls, 1)
	assert.Equal(t, runrecord.Adopted, record.Runs[0].Calls[0].Class)
	var skipped, evicted int
	for _, event := range record.Runs[0].Events {
		if event.Type != hooks.ContextCompacted && event.Type != hooks.ContextCompactionSkipped {
			continue
		}
		var compacted hooks.ContextCompactionEvent
		require.NoError(t, json.Unmarshal(event.Payload, &compacted))
		if compacted.Kind == "empty" {
			skipped++
			assert.Equal(t, 1, compacted.Step)
		}
		if compacted.Tier == string(compaction.Evict) {
			evicted++
			assert.Equal(t, 2, compacted.Step)
		}
	}
	assert.Equal(t, 1, skipped)
	assert.Equal(t, 1, evicted, "receipt-only eviction remains available to the finalizer")
}

func (w *summaryWorkflowContext) Now() time.Time {
	if w.now.IsZero() {
		return time.Unix(0, 0)
	}
	return w.now
}
