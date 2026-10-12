package runtime

import (
	"fmt"
	"slices"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
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
	if !base.contextSettings.Threshold || base.contextSettings.MaxTier == compaction.Clear || base.contextSettings.MaxTier == compaction.Evict {
		return nil
	}
	if !base.contextSettings.Summary.Enabled {
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
	in := &SummarizeContextInput{AgentID: input.AgentID, RunID: input.RunID, Scope: input.RunContext.RootRunID, SessionID: input.RunContext.SessionID, TurnID: input.RunContext.TurnID, Labels: input.RunContext.Labels, Step: input.Step, ViewRef: original, From: max(original.TailFrom, 1), To: input.View.TailFrom, Budget: input.View.Budget, Rate: compaction.SummaryRate(base.compactionState), ContextSettings: base.contextSettings, ContextFingerprint: base.contextFingerprint, ContextModel: base.contextSettings.Summary.Model, PromptID: prompt.Ident(string(input.AgentID) + ".context.summary"), Settings: base.contextSettings.Summary.Settings}
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
		stampCompactionEvent(base, disabled)
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
	stampCompactionEvent(base, event)
	event.Latency = out.Latency
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
	base.compactionState.Recall = base.compactionState.Recall.Refilled(base.contextSettings)
	base.compactionDecision.State = base.compactionState
	base.recallEpisode++
	base.recallExhausted = nil
	input.View = view
	return
}
