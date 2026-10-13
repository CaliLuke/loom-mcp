package runtime

import (
	"errors"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// compactionModels is stable across map insertion order and worker configuration.
func compactionModels(state compaction.State) []api.ModelContext {
	names := make([]string, 0, len(state.Models))
	for name := range state.Models {
		names = append(names, name)
	}
	slices.Sort(names)
	models := make([]api.ModelContext, 0, len(names))
	for _, name := range names {
		model := state.Models[name]
		models = append(models, api.ModelContext{Model: name, Rate: model.Rate, Calibrated: model.Calibrated, Budget: model.Budget})
	}
	return models
}

// prepareCompaction decides from adopted evidence before every schedule, including finalize.
func prepareCompaction(base *workflowPlanInput, input *PlanActivityInput) {
	input.ChainPos, input.ContinuedFrom = base.Pos, base.ContinuedFrom
	base.summaryApplied = false
	base.compactionDecision = compaction.Decision{}
	input.View.ContextFingerprint = base.contextFingerprint
	input.Models = compactionModels(base.compactionState)
	if base.contextSettings.GuardMargin.Bytes == 0 {
		if base.contextSettings.Default {
			base.contextSettings = compaction.Merge(base.contextSettings, nil)
		} else {
			base.contextSettings = resolvedContextSettings(base.compaction, base.recovery)
		}
	}
	input.ContextSettings = plannerContextSettings(base.contextSettings)
	if !compaction.On(base.compactionState, base.Entries, base.contextSettings) || input.Overflow != nil {
		return
	}
	state := base.compactionState
	if input.Recovery != nil {
		// Correction retries keep the last adopted view; threshold transformations
		// and their decision state are discarded on this schedule.
		state.OpenFrom = 1
	}
	settings := base.contextSettings
	settings.MaxTier = compaction.EffectiveTier(state, settings)
	decision := compaction.Decide(state, base.Entries, input.View, settings)
	if input.Recovery != nil {
		decision.View = input.View
		decision.State = base.compactionState
		decision.Tier = compaction.None
		decision.Skipped = ""
	}
	base.compactionDecision = decision
	input.View = decision.View
	input.Models = compactionModels(state)
}

func compactionRecord(wf engine.WorkflowContext, base *workflowPlanInput, input PlanActivityInput, out *PlanActivityOutput, view index.ViewRef) hooks.Event {
	if base.summaryApplied || !compaction.On(base.compactionState, base.Entries, base.contextSettings) || out.Recovery != nil {
		return nil
	}
	d := base.compactionDecision
	if d.Tier == compaction.Clear || d.Tier == compaction.Evict {
		units, calls := compactionCounts(base.Entries, max(base.compactionView.TailFrom, 1), view.TailFrom)
		return stampCompactionEvent(base, hooks.NewContextCompactionEvent(input.RunID, input.AgentID, input.RunContext.SessionID, input.Step, hooks.ContextCompacted, "threshold", string(d.Tier), units, calls, d.Before, d.After, view.Checkpoint, wf.Now()))
	}
	if d.Skipped != "" {
		if d.Skipped == compaction.NoProgress && base.compactionSuppressed && d.State.NoProgressAt == base.compactionState.NoProgressAt {
			return nil
		}
		return stampCompactionEvent(base, hooks.NewContextCompactionEvent(input.RunID, input.AgentID, input.RunContext.SessionID, input.Step, hooks.ContextCompactionSkipped, string(d.Skipped), "", 0, 0, d.Before, d.After, view.Checkpoint, wf.Now()))
	}
	return nil
}

func (r *Runtime) adoptCompaction(wf engine.WorkflowContext, base *workflowPlanInput, input PlanActivityInput, out *PlanActivityOutput, view index.ViewRef) error {
	if out.Recovery == nil {
		if input.Recovery == nil {
			base.compactionSuppressed = base.compactionDecision.Skipped == compaction.NoProgress
		}
		if compaction.On(base.compactionState, base.Entries, base.contextSettings) && input.Overflow == nil {
			base.compactionState = base.compactionDecision.State
		}
		if !base.summaryApplied && (base.compactionDecision.Tier == compaction.Clear || base.compactionDecision.Tier == compaction.Evict) {
			base.compactionState.Recall = base.compactionState.Recall.Refilled(base.contextSettings)
			base.recallEpisode++
			base.recallExhausted = nil
		}
		if out.View != nil {
			base.compactionState.Rendered = compaction.RenderedCheckpoint{Hash: string(out.View.Checkpoint), TailFrom: view.TailFrom, Budget: view.Budget, Bytes: out.View.CheckpointBytes}
		}
	}
	var err error
	base.compactionState, err = compaction.ObserveEffective(base.compactionState, out.Facts, base.contextSettings)
	if err != nil {
		return errors.Join(engine.NewNonRetryableApplicationError(err.Error(), "context_settings", err, err), r.publishCompactionDiagnostics(wf, base, input))
	}
	if err := r.publishRecallDisabled(wf, base, input, view); err != nil {
		return err
	}

	return r.publishCompactionDiagnostics(wf, base, input)
}

func (r *Runtime) publishCompactionDiagnostics(wf engine.WorkflowContext, base *workflowPlanInput, input PlanActivityInput) error {
	flags := base.compactionState.Flags
	diagnostics := []struct {
		kind            hooks.EventType
		seen, published *bool
	}{
		{hooks.ContextUnmeasured, &flags.Unmeasured, &base.compactionFlags.Unmeasured},
		{hooks.ContextLimitsUnknown, &flags.LimitsUnknown, &base.compactionFlags.LimitsUnknown},
		{hooks.ContextBudgetTooSmall, &flags.BudgetTooSmall, &base.compactionFlags.BudgetTooSmall},
	}
	for _, diagnostic := range diagnostics {
		if *diagnostic.seen && !*diagnostic.published {
			event := hooks.NewContextDiagnosticEvent(input.RunID, input.AgentID, input.RunContext.SessionID, diagnostic.kind, wf.Now())
			if diagnostic.kind == hooks.ContextLimitsUnknown && base.compactionState.InvalidLimitsModel != "" {
				event.Reason = "invalid_override"
				event.Model = base.compactionState.InvalidLimitsModel
			}
			if err := r.publishHook(wf.Context(), event, input.RunContext.TurnID); err != nil {
				return fmt.Errorf("publish context diagnostic: %w", err)
			}
			*diagnostic.published = true
		}
	}
	return nil
}

func (r *Runtime) publishRecallDisabled(wf engine.WorkflowContext, base *workflowPlanInput, input PlanActivityInput, view index.ViewRef) error {
	if base.recovery.DisableRecall && (view.Checkpoint != nil || view.ClearedThrough > 0 || view.PreviewThrough > 0 || view.ForceClearThrough > 0) && !base.recallDisabledPublished {
		event := hooks.NewContextDiagnosticEvent(input.RunID, input.AgentID, input.RunContext.SessionID, hooks.ContextRecallDisabled, wf.Now())
		if err := r.publishHook(wf.Context(), event, input.RunContext.TurnID); err != nil {
			return err
		}
		base.recallDisabledPublished = true
	}
	return nil
}

// compactionCounts derives event totals from the immutable evicted prefix.
func compactionCounts(entries []index.MessageEntry, from, to index.MessageID) (int, int) {
	units, calls := 0, 0
	for _, unit := range checkpoint.Units(entries, index.RecordedRole, 0) {
		if unit.From >= from && unit.Through < to {
			units++
		}
	}
	for _, entry := range entries {
		if entry.ID >= from && entry.ID < to {
			calls += len(entry.Calls)
		}
	}
	return units, calls
}

// stampCompactionEvent binds telemetry and offline evidence to effective settings.
func stampCompactionEvent(base *workflowPlanInput, event *hooks.ContextCompactionEvent) *hooks.ContextCompactionEvent {
	event.Fingerprint = base.contextFingerprint
	event.Pretend = base.contextSettings.ContextWindow > 0
	event.EffectiveWindow = base.compactionState.EffectiveWindow
	if event.Pretend {
		event.EffectiveWindow = base.contextSettings.ContextWindow
	}
	return event
}

// plannerContextSettings sends only guard inputs in the bounded planner envelope.
// Canonical records retain the full workflow-stamped settings and fingerprint.
func plannerContextSettings(settings compaction.Settings) compaction.Settings {
	return compaction.Settings{Default: settings.Default, KeepNonTextResident: settings.KeepNonTextResident, Threshold: settings.Threshold, GuardMargin: settings.GuardMargin, TriggerAt: settings.TriggerAt, DisableRecall: settings.DisableRecall}
}
