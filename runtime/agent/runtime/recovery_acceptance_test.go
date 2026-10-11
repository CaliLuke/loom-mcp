package runtime

import (
	"context"
	jsonopts "encoding/json"
	"encoding/json/v2"
	"strings"
	"testing"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/overflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runrecord"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOverflowLargeSummarySwitchToSmallModel(t *testing.T) {
	rt := New()
	base := compactionFixture(t, rt.RunStore, 3)
	summary, err := rt.RunStore.Put(t.Context(), "root", []byte(strings.Repeat("summary ", 4000)))
	require.NoError(t, err)
	// Inject a 16k-token checkpoint produced by a preceding large-model step.
	record := index.CheckpointRecord{Summary: &summary, SummaryRun: "run", Through: 3, Chain: base.Entries[2].Chain}
	hash := compactionPut(t, rt.RunStore, record)
	base.compactionView = index.ViewRef{Checkpoint: &hash, TailFrom: 4, Budget: index.RenderBudget{KeepUserTokens: 10000, MaxSummaryTokens: 16000, ReceiptTokens: 6000, FramingTokens: 200}}
	provider := &overflowProvider{window: 8000, realWindow: 8000}
	require.NoError(t, rt.RegisterModel("small", provider))
	var loaded [][]*model.Message
	rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
		loaded = append(loaded, cloneMessages(in.Messages))
		client, ok := in.Agent.ModelClient("small")
		require.True(t, ok)
		_, err := client.Complete(ctx, &model.Request{Messages: in.Messages})
		if err != nil {
			return nil, err
		}
		return oracleFinal(), nil
	}}}
	wf := &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}
	caps := policy.CapsState{}
	out, err := rt.runPlanActivityRecovering(wf, base, "plan", engine.ActivityOptions{}, PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}, time.Time{}, &caps, nil)
	require.NoError(t, err)
	require.NotNil(t, out.Result)
	require.Len(t, provider.sizes, 2)
	assert.Greater(t, provider.sizes[0], 8000)
	assert.LessOrEqual(t, provider.sizes[1], 8000)
	assert.Equal(t, 800, base.compactionView.Budget.MaxSummaryTokens)
	assert.Equal(t, policy.CapsState{}, caps)
	text, err := contextmeasure.MeasureMessages(loaded[1][:1])
	require.NoError(t, err)
	assert.LessOrEqual(t, text.Bytes, checkpoint.MaxBytes(base.compactionView.Budget))
	rr, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
	require.NoError(t, err)
	assert.Len(t, rr.Runs[0].Calls, 2)
}

func TestOverflowRecoveryEndsWithSpentBudget(t *testing.T) {
	rt := New()
	base := compactionFixture(t, rt.RunStore, 1)
	// The only result is Keep, so repeated recoveries must preview, not clear it.
	entry := &base.Entries[2]
	entry.Hash = compactionPut(t, rt.RunStore, strings.Repeat("large result ", 4000))
	entry.Bytes, entry.Keep = 52000, true
	entry.Result = &index.ResultProjection{ContentBytes: 52000}
	chain, err := index.ComputeChain(base.Entries[1].Chain, *entry)
	require.NoError(t, err)
	entry.Chain = chain
	failure := model.NewProviderError("fake", "complete", 400, model.ProviderErrorKindContextOverflow, "too_large", "always rejects", "", false, nil)
	calls := 0
	provider := &recordingFactsClient{recordingTestClient: &recordingTestClient{store: &recordingTestStore{Store: rt.RunStore}, err: failure}, limits: model.ContextLimits{ContextWindow: 8000}, resolved: "small"}
	require.NoError(t, rt.RegisterModel("fake", provider))
	rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
		calls++
		client, ok := in.Agent.ModelClient("fake")
		require.True(t, ok)
		_, err := client.Complete(ctx, &model.Request{Messages: in.Messages})
		return nil, err
	}}}
	wf := &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}
	caps := policy.CapsState{}
	_, err = rt.runPlanActivityRecovering(wf, base, "plan", engine.ActivityOptions{}, PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}, time.Time{}, &caps, nil)
	require.ErrorIs(t, err, ErrContextOverflowBudget)
	assert.Equal(t, 3, calls)
	var typed *ContextOverflowError
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, "budget", typed.Reason)
	rr, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
	require.NoError(t, err)
	require.Len(t, rr.Runs[0].Calls, 3)
	for i := 1; i < 3; i++ {
		assert.Less(t, rr.Runs[0].Calls[i].Completion.Facts.TextBytes, rr.Runs[0].Calls[i-1].Completion.Facts.TextBytes)
	}
	var overflows []hooks.ContextOverflowEvent
	for _, event := range rr.Runs[0].Events {
		if event.Type == hooks.ContextOverflow {
			var output hooks.ContextOverflowEvent
			require.NoError(t, json.Unmarshal(event.Payload, &output, jsonopts.FormatDurationAsNano(true)))
			overflows = append(overflows, output)
		}
	}
	require.Len(t, overflows, 3)
	assert.Equal(t, "failed:budget", overflows[2].Outcome)
	assert.Equal(t, policy.CapsState{}, caps)
}

func TestRecoveryPreviewColdWarmAndKeep(t *testing.T) {
	rt := New()
	base := compactionFixture(t, rt.RunStore, 1)
	entry := &base.Entries[2]
	entry.Hash = compactionPut(t, rt.RunStore, strings.Repeat("UTF-8 évidence ", 3000))
	entry.Bytes, entry.Keep = 48000, true
	entry.Result = &index.ResultProjection{ContentBytes: 48000}
	chain, err := index.ComputeChain(base.Entries[1].Chain, *entry)
	require.NoError(t, err)
	entry.Chain = chain
	view := base.view()
	previous := index.ViewRef{}
	for _, tokens := range []int{512, 256, 128, 0} {
		view.PreviewThrough = 3
		view.Budget.PreviewTokens = tokens
		if tokens == 0 {
			view.ForceClearThrough = 3
		}
		warm, err := rt.loadTestView(t.Context(), "root", "run", view)
		require.NoError(t, err)
		view.IndexHead, view.Pending = warm.Head, nil
		coldRT := New(WithRunStore(rt.RunStore))
		cold, err := coldRT.loadTestView(t.Context(), "root", "run", view)
		require.NoError(t, err)
		again, err := rt.loadTestView(t.Context(), "root", "run", view)
		require.NoError(t, err)
		assert.Equal(t, canonicalIndexMessages(t, warm.Messages), canonicalIndexMessages(t, cold.Messages))
		assert.Equal(t, canonicalIndexMessages(t, warm.Messages), canonicalIndexMessages(t, again.Messages))
		part := warm.Messages[len(warm.Messages)-1].Parts[0].(model.ToolResultPart)
		if tokens > 0 {
			assert.LessOrEqual(t, len(part.Content.(string)), 2*tokens)
		} else {
			assert.Contains(t, part.Content, "result cleared")
		}
		assert.GreaterOrEqual(t, view.PreviewThrough, previous.PreviewThrough)
		assert.GreaterOrEqual(t, view.ForceClearThrough, previous.ForceClearThrough)
		previous = view
	}
	assert.True(t, base.Entries[2].Keep)
}

// The compact-during-recovery fault removes the overflow marker before scheduling.
// Its threshold decision must differ, proving this fixture detects an intervening
// transformation rather than merely checking an idle compaction configuration.
func TestOverflowRetrySkipsThresholdCompaction(t *testing.T) {
	rt := New()
	base := compactionFixture(t, rt.RunStore, 3)
	base.compaction = &ContextCompaction{TriggerAt: contextmeasure.Rate{Tokens: 1, Bytes: 100}, KeepRecentTokens: 1, MinSavingTokens: 1, ProtectRecentToolTokens: 1}
	base.compactionState = compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{{Model: "small", ViewBearing: true, ContextWindow: 8000}})
	input := PlanActivityInput{View: base.view(), Overflow: &overflow.Step{ProviderLeft: 1}}
	original := input.View
	prepareCompaction(base, &input)
	assert.Equal(t, original, input.View, "the retry sends exactly the recomputed view")
	assert.Equal(t, compaction.Decision{}, base.compactionDecision)
	mutant := input
	mutant.Overflow = nil
	prepareCompaction(base, &mutant)
	assert.NotEqual(t, original, mutant.View, "compact-during-recovery fault is caught")
}

// This test double mutates only the recovery-loop transition: it restores the
// failed view and resets the provider budget. The production decision terminates;
// the unbounded fault fails the same finite action bound.
func TestOverflowRecoveryUnboundedFault(t *testing.T) {
	entries := []index.MessageEntry{{ID: 1, Kind: index.Assistant, Role: "assistant", Bytes: 50}, {ID: 2, Kind: index.ToolResult, Role: "user", Bytes: 52000, Keep: true, ToolUseIDs: []string{"kept"}}}
	for _, mutant := range []bool{false, true} {
		failure := contextmeasure.ContextOverflow{Source: contextmeasure.OverflowProvider, Failed: contextmeasure.RequestFacts{Model: "small", ViewBearing: true, ContextWindow: 8000, TextBytes: 52050, ViewTextBytes: 52050}}
		step := overflow.Step{ProviderLeft: 2, Rejected: make(map[string]int)}
		rejected := make(map[string]runstore.CallKey)
		view := index.ViewRef{}
		terminated, actions := false, 0
		for actions < 16 {
			actions++
			d := decideOverflow(compaction.State{}, entries, view, failure, compaction.Settings{}, &step, rejected)
			if d.reason != "" {
				terminated = true
				break
			}
			if mutant {
				step.ProviderLeft = 2
				view = index.ViewRef{}
				continue
			}
			view = d.recovery.View
			failure.Failed.TextBytes, failure.Failed.ViewTextBytes = d.recovery.After*3, d.recovery.After*3
		}
		if mutant {
			assert.False(t, terminated, "unbounded fault exceeds the termination bound")
			assert.Equal(t, 16, actions)
		} else {
			assert.True(t, terminated)
			assert.Equal(t, 3, actions, "two retries after observed provider overflow")
		}
	}
}

func TestOverflowCommitsThresholdEvictionAndRecovery(t *testing.T) {
	rt := New()
	base := compactionFixture(t, rt.RunStore, 3)
	base.RunContext.Attempt = 1
	base.compaction = &ContextCompaction{TriggerAt: contextmeasure.Rate{Tokens: 1, Bytes: 100}, KeepRecentTokens: 1, MinSavingTokens: 1, ProtectRecentToolTokens: 1}
	base.compactionState = compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{{Model: "small", ViewBearing: true, ContextWindow: 8000}})
	base.compactionState.Recall = compaction.NewRecall(compaction.Settings{})
	base.compactionSuppressed = true
	entry := &base.Entries[6]
	entry.Hash = compactionPut(t, rt.RunStore, strings.Repeat("large result ", 4000))
	entry.Bytes, entry.Keep, entry.Result = 52000, true, &index.ResultProjection{ContentBytes: 52000}
	chain, err := index.ComputeChain(base.Entries[5].Chain, *entry)
	require.NoError(t, err)
	entry.Chain = chain
	provider := &overflowProvider{window: 8000, realWindow: 8000}
	require.NoError(t, rt.RegisterModel("fake", provider))
	var retryRecall compaction.RecallAllowances
	var retrySuppressed bool
	rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
		if provider.calls > 0 {
			retryRecall, retrySuppressed = base.compactionState.Recall, base.compactionSuppressed
		}
		client, ok := in.Agent.ModelClient("fake")
		require.True(t, ok)
		_, err := client.Complete(ctx, &model.Request{Messages: in.Messages})
		if err != nil {
			return nil, err
		}
		return oracleFinal(), nil
	}}}
	wf := &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}
	caps := policy.CapsState{}
	out, err := rt.runPlanActivityRecovering(wf, base, "plan", engine.ActivityOptions{}, PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}, time.Time{}, &caps, nil)
	require.NoError(t, err)
	require.NotNil(t, out.Result)
	require.Len(t, provider.sizes, 2)
	assert.Greater(t, provider.sizes[0], 8000)
	assert.LessOrEqual(t, provider.sizes[1], 8000)
	assert.Equal(t, compaction.RecallAllowances{Discovery: 40, Reads: 40}, retryRecall, "both committed compactions refill before the retry")
	assert.False(t, retrySuppressed, "threshold adoption clears previous suppression")
	assert.Equal(t, 2, base.recallEpisode)
	record, err := runrecord.Read(t.Context(), rt.RunStore, rt.RunEventStore, "root", "run")
	require.NoError(t, err)
	rr := record.Runs[0]
	assert.Equal(t, retryRecall, rr.Recall)
	assert.Len(t, rr.Calls, 2)
	assert.Equal(t, base.IndexHead, rr.IndexHead)
	assert.NotEmpty(t, rr.Checkpoints)
	var compacted []hooks.ContextCompactionEvent
	var overflowEvent hooks.ContextOverflowEvent
	var compactedKeys []string
	for _, event := range rr.Events {
		if event.Type == hooks.ContextOverflow {
			require.NoError(t, json.Unmarshal(event.Payload, &overflowEvent, jsonopts.FormatDurationAsNano(true)))
		}
		if event.Type == hooks.ContextCompacted {
			var compactionEvent hooks.ContextCompactionEvent
			require.NoError(t, json.Unmarshal(event.Payload, &compactionEvent))
			compacted = append(compacted, compactionEvent)
			compactedKeys = append(compactedKeys, event.EventKey)
		}
	}
	require.Len(t, compacted, 2)
	assert.Equal(t, "threshold", compacted[0].Reason)
	assert.Equal(t, "evict", compacted[0].Tier)
	assert.Equal(t, "overflow", compacted[1].Reason)
	assert.Equal(t, "preview", compacted[1].Tier)
	assert.Equal(t, []string{"context_compacted:run:1", "context_compacted:run:1:overflow"}, compactedKeys)
	assert.Equal(t, index.MessageID(6), overflowEvent.ViewRef.TailFrom)
	assert.Equal(t, compacted[0].Checkpoint, overflowEvent.ViewRef.Checkpoint)
	assert.Equal(t, "recovered", overflowEvent.Outcome)
	assert.Equal(t, wf.plannerCalls[0].Input.View.TailFrom, overflowEvent.ViewRef.TailFrom)
	assert.Equal(t, base.compactionView.PreviewThrough, wf.plannerCalls[1].Input.View.PreviewThrough)
}

func TestOverflowMinimalErrorUsesThresholdAdoptedView(t *testing.T) {
	rt := New()
	base := compactionFixture(t, rt.RunStore, 0)
	base.RunContext.Attempt = 1
	for _, message := range []*model.Message{{Role: "assistant", Parts: []model.Part{model.TextPart{Text: strings.Repeat("old", 20000)}}}, contentMessage("continue"), {Role: "assistant", Parts: []model.Part{model.TextPart{Text: "newest"}}}} {
		size, err := contextmeasure.MeasureMessages([]*model.Message{message})
		require.NoError(t, err)
		require.NoError(t, base.appendEntry(index.MessageEntry{Kind: index.Message, Role: message.Role, Hash: compactionPut(t, rt.RunStore, message), Bytes: size.Bytes}))
	}
	base.compaction = &ContextCompaction{TriggerAt: contextmeasure.Rate{Tokens: 1, Bytes: 100}, KeepRecentTokens: 1}
	base.recovery.DisableRecall = true
	base.compactionState = compaction.Observe(compaction.State{}, []contextmeasure.RequestFacts{{Model: "small", ViewBearing: true, ContextWindow: 8000}})
	provider := &overflowProvider{window: 8000, realWindow: 8000, failFirst: true}
	require.NoError(t, rt.RegisterModel("fake", provider))
	rt.agents["agent"] = AgentRegistration{ID: "agent", Planner: &stubPlanner{start: func(ctx context.Context, in *planner.PlanInput) (*planner.PlanResult, error) {
		client, ok := in.Agent.ModelClient("fake")
		require.True(t, ok)
		fixed := &model.Message{Role: "system", Parts: []model.Part{model.TextPart{Text: strings.Repeat("fixed", 1000)}}}
		_, err := client.Complete(ctx, &model.Request{Messages: append([]*model.Message{fixed}, in.Messages...)})
		return nil, err
	}}}
	wf := &testWorkflowContext{ctx: engine.WithActivityAttempt(t.Context(), 1), runtime: rt, hookRuntime: rt}
	caps := policy.CapsState{}
	_, err := rt.runPlanActivityRecovering(wf, base, "plan", engine.ActivityOptions{}, PlanActivityInput{AgentID: "agent", RunID: "run", RunContext: base.RunContext, View: base.view()}, time.Time{}, &caps, nil)
	require.ErrorIs(t, err, ErrContextBudgetExceeded)
	var typed *ContextOverflowError
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, "minimal", typed.Reason)
	assert.Equal(t, index.MessageID(4), typed.FirstMessage)
	assert.Equal(t, 1667, typed.FixedTokens)
	assert.Equal(t, base.compactionView.TailFrom, typed.FirstMessage)
	assert.Len(t, wf.plannerCalls, 1)
}
