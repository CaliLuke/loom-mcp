package runtime

import (
	"fmt"
	"slices"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// Summary folds can each make three provider attempts. Their activity deadline
// respects the run's active budget; finalizers never summarize.
const (
	summaryActivityTimeout = 24 * defaultPlanActivityTimeout
	summaryStale           = "stale"
	summaryLimitsUnknown   = "summariser_limits_unknown"
)

// prepareWorkflowSummary adopts the summary attempt before any planner command.
func (r *Runtime) prepareWorkflowSummary(wf engine.WorkflowContext, base *workflowPlanInput, input *PlanActivityInput, deadline time.Time) error {
	if base.compaction == nil || base.compaction.ContextModel == nil {
		if !base.summariesOffLogged && r.logger != nil {
			r.logger.Info(wf.Context(), "context summaries are off: no ContextModel", "run_id", input.RunID)
			base.summariesOffLogged = true
		}
		return nil
	}
	if base.compactionDecision.Tier != compaction.Evict || base.compactionState.Summary.Disabled || input.Finalize != nil || input.Overflow != nil || input.Recovery != nil {
		return nil
	}
	options, err := capPlanActivityOptions(wf, engine.ActivityOptions{StartToCloseTimeout: summaryActivityTimeout}, deadline)
	if err != nil {
		return err
	}
	original := base.view()
	in := &SummarizeContextInput{AgentID: input.AgentID, RunID: input.RunID, Scope: input.RunContext.RootRunID, SessionID: input.RunContext.SessionID, TurnID: input.RunContext.TurnID, Labels: input.RunContext.Labels, Step: input.Step, ViewRef: original, From: max(original.TailFrom, 1), To: input.View.TailFrom, Budget: input.View.Budget, Rate: summaryEstimateRate(base.compactionState), ContextModel: *base.compaction.ContextModel, PromptID: prompt.Ident(string(input.AgentID) + ".context.summary"), Settings: base.compaction.Summary}
	out, err := wf.ExecuteSummarizeContextActivity(wf.Context(), engine.SummarizeContextActivityCall{Name: SummarizeContextActivityName(input.AgentID), Input: in, Options: options})
	if err != nil {
		return fmt.Errorf("summarize context: %w", err)
	}
	return r.adoptWorkflowSummary(wf, base, input, in, out)
}

// adoptWorkflowSummary binds application to the original cut; even discarded
// outputs adopt all their started call keys through the canonical record event.
func (r *Runtime) adoptWorkflowSummary(wf engine.WorkflowContext, base *workflowPlanInput, input *PlanActivityInput, in *SummarizeContextInput, out *SummarizeContextOutput) error {
	if out == nil {
		return fmt.Errorf("summarize context: missing activity output")
	}
	if err := validateCompactionFacts(&PlanActivityOutput{CallKeys: out.CallKeys, Facts: out.Facts}); err != nil {
		return err
	}
	view, event, err := summaryAdoptionEvent(wf, base, input, in, out)
	if err != nil {
		return err
	}
	kind := event.Kind
	if err := r.publishHook(wf.Context(), event, input.RunContext.TurnID); err != nil {
		return err
	}
	next := base.compactionState.Summary.Reject(kind)
	if !base.compactionState.Summary.Disabled && next.Disabled {
		disabled := hooks.NewContextCompactionEvent(input.RunID, input.AgentID, input.RunContext.SessionID, input.Step, hooks.ContextCompactionSkipped, "summaries_disabled", "", 0, 0, 0, 0, base.compactionView.Checkpoint, wf.Now())
		disabled.SetEventKey(disabled.EventKey() + ":disabled")
		if err := r.publishHook(wf.Context(), disabled, input.RunContext.TurnID); err != nil {
			return err
		}
	}
	base.compactionState.Summary = next
	base.compactionDecision.State.Summary = next
	base.applySummaryView(input, out, kind, view)
	return nil
}

// summaryEstimateRate selects the largest recorded text rate without map-order influence.
func summaryEstimateRate(state compaction.State) contextmeasure.Rate {
	rate := contextmeasure.Rate{Tokens: 1, Bytes: 3}
	found := false
	for _, name := range compactionModels(state) {
		if !state.Models[name.Model].Known || name.Rate.Bytes <= 0 {
			continue
		}
		if !found || int64(name.Rate.Tokens)*int64(rate.Bytes) > int64(rate.Tokens)*int64(name.Rate.Bytes) {
			rate = name.Rate
			found = true
		}
	}
	return rate
}

// summaryAdoptionEvent constructs an immutable canonical record at the requested cut.
func summaryAdoptionEvent(wf engine.WorkflowContext, base *workflowPlanInput, input *PlanActivityInput, in *SummarizeContextInput, out *SummarizeContextOutput) (index.ViewRef, *hooks.ContextCompactionEvent, error) {
	kind := out.Kind
	if out.From != max(base.compactionView.TailFrom, 1) || out.From != in.From || out.To != in.To {
		kind = summaryStale
	}
	if kind == "" && out.Checkpoint == nil {
		return index.ViewRef{}, nil, fmt.Errorf("summarize context: successful output has no checkpoint")
	}
	eventType, reason, tier := hooks.ContextCompacted, "threshold", "summary"
	view := input.View
	if kind != "" {
		eventType, reason, tier = hooks.ContextCompactionSkipped, "rejected", ""
		if kind == summaryStale || kind == summaryLimitsUnknown {
			reason = kind
		}
	} else {
		view.Checkpoint = out.Checkpoint
		view.Pinned = slices.Clone(out.Pinned)
		view.IndexHead = out.IndexHead
		view.Pending = nil
	}
	d := base.compactionDecision
	event := hooks.NewContextCompactionEvent(input.RunID, input.AgentID, input.RunContext.SessionID, input.Step, eventType, reason, tier, 0, 0, d.Before, d.After, base.compactionView.Checkpoint, wf.Now())
	event.Kind, event.CallKeys, event.Model, event.Usage, event.Chunks, event.UnsummarisedBytes, event.LooseRefs, event.Prompt = kind, slices.Clone(out.CallKeys), in.ContextModel.ID, out.Usage, out.Chunks, out.UnsummarisedBytes, out.LooseRefs, out.Prompt
	settings := base.contextSettings
	event.ContextSettings = &settings
	if kind == "" {
		event.Units, event.Calls = compactionCounts(base.Entries, in.From, in.To)
		event.Checkpoint = view.Checkpoint
		event.ViewRef = &view
	}
	return view, event, nil
}

// applySummaryView changes only workflow-owned state after canonical adoption.
func (base *workflowPlanInput) applySummaryView(input *PlanActivityInput, out *SummarizeContextOutput, kind string, view index.ViewRef) {
	if kind == summaryStale {
		input.View = base.view()
		base.compactionDecision = compaction.Decision{View: input.View, State: base.compactionState, Tier: compaction.None}
		return
	}
	// The activity commits Pending even on a content rejection, except when limits
	// were unknown and no content was read. Do not drop still-pending entries then.
	if out.IndexHead != "" && out.IndexHead != base.IndexHead {
		base.adoptHead(out.IndexHead)
		input.View.IndexHead = out.IndexHead
		input.View.Pending = nil
	}
	if kind != "" {
		return
	}
	base.summaryApplied = true
	base.compactionView = view
	base.compactionState = base.compactionDecision.State
	base.compactionState.Recall = base.compactionState.Recall.Refilled(input.ContextSettings.Settings)
	base.compactionDecision.State = base.compactionState
	base.recallEpisode++
	base.recallExhausted = nil
	input.View = view
	return
}
