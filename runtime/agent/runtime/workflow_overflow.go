package runtime

import (
	"errors"
	"fmt"
	"maps"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/compaction"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/overflow"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/checkpoint"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

type overflowDecision struct {
	outcome, reason string
	recovery        *compaction.Recovery
	shortfall       *compaction.BudgetShortfallError
	calls           []runstore.CallKey
}

const (
	overflowReasonMinimal   = "minimal"
	overflowOutcomeGuardOff = "guard_off"
)

// recoverOverflow adopts every call before deciding on a retry. Provider and
// advisory guard budgets are independent of output-validation recovery turns.
func (r *Runtime) recoverOverflow(wf engine.WorkflowContext, base *workflowPlanInput, input *PlanActivityInput, out *PlanActivityOutput, step *overflow.Step, rejected map[string]runstore.CallKey, nextAttempt *int) error {
	input.Step = base.step
	input.ContextSettings = plannerContextSettings(base.contextSettings)
	input.View.ContextFingerprint = base.contextFingerprint
	view, adoption := plannerAdoptionEvent(wf, base, *input, out)
	state := base.compactionState
	if base.compaction != nil && input.Overflow == nil {
		state = base.compactionDecision.State
	}
	var err error
	state, err = compaction.ObserveEffective(state, out.Facts, base.contextSettings)
	if err != nil {
		base.compactionState = state
		return errors.Join(engine.NewNonRetryableApplicationError(err.Error(), "context_settings", err, err), r.publishCompactionDiagnostics(wf, base, *input))
	}
	failure := *out.ContextOverflow
	decision := decideOverflow(state, base.Entries, view, failure, base.contextSettings, step, rejected)
	if err := r.commitOverflow(wf, base, *input, out, view, adoption, state, failure, decision); err != nil {
		return err
	}
	if decision.reason != "" {
		return decision.applicationError(*input, view, state, failure)
	}
	snapshot := *step
	snapshot.Rejected = maps.Clone(step.Rejected)
	input.View, input.Overflow = base.view(), &snapshot
	input.RunContext.Attempt++
	if nextAttempt != nil {
		*nextAttempt = input.RunContext.Attempt + 1
	}
	return nil
}

// decideOverflow spends only observed provider rejections, never guard attempts.
func decideOverflow(state compaction.State, entries []index.MessageEntry, view index.ViewRef, failure contextmeasure.ContextOverflow, settings compaction.Settings, step *overflow.Step, rejected map[string]runstore.CallKey) overflowDecision {
	fact := failure.Failed
	d := overflowDecision{outcome: "recovered"}
	d.calls = []runstore.CallKey{fact.Call}
	if previous, ok := rejected[fact.Model]; ok {
		d.calls = append([]runstore.CallKey{previous}, d.calls...)
	}
	if failure.Source == contextmeasure.OverflowGuard {
		step.GuardUsed = true
	}
	switch {
	case failure.Source == contextmeasure.OverflowNoProgress:
		d.reason = contextmeasure.OverflowNoProgress
	case d.unavailableRecovery(failure, view):
		if failure.Source == contextmeasure.OverflowGuard {
			step.GuardOff = true
		}
	case !fact.ViewBearing && failure.Source == contextmeasure.OverflowGuard:
		step.GuardOff, d.outcome = true, overflowOutcomeGuardOff
	case !fact.ViewBearing:
		d.reason = "not_view_bearing"
	default:
		d.decideViewRecovery(state, entries, view, failure, settings, step, rejected)
	}
	if d.reason != "" {
		d.outcome = "failed:" + d.reason
	}
	return d
}

func (r *Runtime) commitOverflow(wf engine.WorkflowContext, base *workflowPlanInput, input PlanActivityInput, out *PlanActivityOutput, view index.ViewRef, adoption *hooks.PlannerStepEvent, state compaction.State, failure contextmeasure.ContextOverflow, decision overflowDecision) error {
	if decision.shortfall != nil {
		state.Flags.BudgetTooSmall = true
	}
	event := hooks.NewContextOverflowEvent(adoption, failure, decision.outcome, decision.recovery)
	publications := []hooks.Event{event}
	threshold := compactionRecord(wf, base, input, out, view)
	if threshold != nil {
		publications = append(publications, threshold)
	}
	if decision.recovery != nil && decision.recovery.Tier != compaction.None {
		recovered := hooks.NewContextCompactionEvent(input.RunID, input.AgentID, input.RunContext.SessionID, input.Step, hooks.ContextCompacted, "overflow", string(decision.recovery.Tier), 0, 0, decision.recovery.Before, decision.recovery.After, decision.recovery.View.Checkpoint, wf.Now())
		if threshold != nil && threshold.Type() == hooks.ContextCompacted {
			// Two committed transformations share this schedule. Preserve the K12
			// threshold identity while giving recovery a distinct deterministic key.
			recovered.SetEventKey(recovered.EventKey() + ":overflow")
		}
		publications = append(publications, stampCompactionEvent(base, recovered))
	}
	if err := r.publishWorkflowHookBatch(wf, input.RunContext.TurnID, publications...); err != nil {
		return fmt.Errorf("commit context overflow: %w", err)
	}
	base.adoptHead(out.IndexHead)
	base.adoptOverflowView(input, view, state, decision, threshold)
	// Observation and diagnostics run for every agent. Do not reapply the
	// threshold decision after adopting the recomputed view.
	if err := r.publishRecallDisabled(wf, base, input, base.compactionView); err != nil {
		return err
	}
	if err := r.publishCompactionDiagnostics(wf, base, input); err != nil {
		return err
	}
	return nil
}

// adoptOverflowView applies each transformation only after its batch commits.
// Threshold adoption precedes the recovery refill, matching the record order.
func (base *workflowPlanInput) adoptOverflowView(input PlanActivityInput, view index.ViewRef, state compaction.State, decision overflowDecision, threshold hooks.Event) {
	base.compactionView, base.compactionState = view, state
	if base.compaction != nil && input.Overflow == nil && input.Recovery == nil {
		base.compactionSuppressed = base.compactionDecision.Skipped == compaction.NoProgress
	}
	if threshold != nil && threshold.Type() == hooks.ContextCompacted {
		base.compactionState.Recall = base.compactionState.Recall.Refilled(base.contextSettings)
		base.recallEpisode++
		base.recallExhausted = nil
	}
	if decision.recovery != nil {
		base.compactionView = decision.recovery.View
		if decision.recovery.Tier != compaction.None {
			base.compactionState.Recall = base.compactionState.Recall.Refilled(base.contextSettings)
			base.recallEpisode++
			base.recallExhausted = nil
		}
	}
}

func (d overflowDecision) applicationError(input PlanActivityInput, view index.ViewRef, state compaction.State, failure contextmeasure.ContextOverflow) error {
	fact := failure.Failed
	typed := &ContextOverflowError{Reason: d.reason, Planner: input.AgentID, Calls: d.calls, Facts: fact, Shortfall: d.shortfall}
	if failure.Provider != nil {
		typed.Cause = failure.Provider.Restore()
	}
	if d.reason == overflowReasonMinimal {
		typed.FirstMessage = max(view.TailFrom, 1)
	}
	rate := state.Models[fact.Model].Rate
	typed.FixedTokens = contextmeasure.Estimate(rate, 0, max(0, fact.TextBytes-fact.ViewTextBytes), max(0, fact.NonText-fact.ViewNonText))
	return engine.NewNonRetryableApplicationError(typed.Error(), "context_overflow."+d.reason, typed, typed)
}

func (d *overflowDecision) decideViewRecovery(state compaction.State, entries []index.MessageEntry, view index.ViewRef, failure contextmeasure.ContextOverflow, settings compaction.Settings, step *overflow.Step, rejected map[string]runstore.CallKey) {
	fact := failure.Failed
	switch {
	case failure.Source == contextmeasure.OverflowProvider && step.ProviderLeft == 0:
		d.reason = "budget"
	case failure.Source == contextmeasure.OverflowProvider && compaction.Minimal(entries, view):
		d.reason = overflowReasonMinimal
	default:
		if failure.Source == contextmeasure.OverflowProvider {
			size := overflow.Size(fact)
			if previous, ok := step.Rejected[fact.Model]; !ok || size < previous {
				step.Rejected[fact.Model], rejected[fact.Model] = size, fact.Call
			}
			step.ProviderLeft--
		}
		computed := compaction.Recover(state, entries, view, failure, settings, true)
		d.recovery = &computed
		if !computed.Reached || computed.After >= computed.Before {
			step.GuardOff, d.outcome = true, overflowOutcomeGuardOff
		}
	}
}

// unavailableRecovery preserves B-derived rendering capacity; a guard alone
// disables itself, whereas an observed provider rejection explains the failure.
func (d *overflowDecision) unavailableRecovery(failure contextmeasure.ContextOverflow, view index.ViewRef) bool {
	fact := failure.Failed
	budget := compaction.RecoveryBudget(fact, view.Budget.PreviewTokens)
	if fact.ContextWindow == 0 || checkpoint.MaxBytes(budget) >= checkpoint.MinCheckpointBytes(budget) {
		return false
	}
	if failure.Source == contextmeasure.OverflowGuard {
		d.outcome = overflowOutcomeGuardOff
	} else {
		d.reason = "budget_too_small"
		d.shortfall = &compaction.BudgetShortfallError{Budget: max(0, fact.ContextWindow-fact.OutputReserve), MaxBytes: checkpoint.MaxBytes(budget), MinBytes: checkpoint.MinCheckpointBytes(budget)}
	}
	return true
}
