package runtime

// workflow_turn.go contains the implementation of a single “tool turn” inside the
// durable workflow plan loop.
//
// Contract:
// - The function in this file is replay-safe: it uses workflow time and publishes
//   hook events deterministically based on inputs.
// - It owns the mechanics of taking planner ToolCalls through policy/confirmation,
//   recording the assistant tool_use turn, executing tools, and producing the next
//   PlanResume request (or finalizing).
// - It may also handle “mixed” turns where the planner returns ToolCalls plus an
//   Await.ExternalTools handshake (execute internal tools first, then pause).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
)

// handleToolTurn executes the planner-returned tool calls for the current turn
// and advances the workflow to the next planner result.
//
// Return contract:
//   - **out != nil**: the run is complete (success/finalized) and the caller must return.
//   - **out == nil && err == nil**: the turn was executed and st was advanced to the next
//     planner result; the caller should continue the loop.
func (r *Runtime) handleToolTurn(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *workflowPlanInput,
	st *runLoopState,
	resumeOpts engine.ActivityOptions,
	toolOpts engine.ActivityOptions,
	deadlines *runDeadlines,
	turnID string,
	parentTracker *childTracker,
	ctrl *interrupt.Controller,
) (*RunOutput, error) {
	result := st.Result
	if deadlines == nil {
		return nil, errors.New("missing run deadlines")
	}
	if out, err := r.enforceToolTurnGuards(wfCtx, reg, input, base, st, turnID, deadlines); err != nil || out != nil {
		return out, err
	}
	turn, out, err := r.prepareAdmittedToolTurn(wfCtx, reg, input, base, st, turnID, parentTracker, ctrl, toolOpts, deadlines)
	if err != nil || out != nil {
		return out, err
	}

	return r.executeAndAdvanceToolTurn(
		wfCtx, reg, input, base, st, resumeOpts, toolOpts, deadlines, turnID, parentTracker, ctrl, result, turn,
	)
}

func (r *Runtime) prepareAdmittedToolTurn(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *workflowPlanInput,
	st *runLoopState,
	turnID string,
	parentTracker *childTracker,
	ctrl *interrupt.Controller,
	toolOpts engine.ActivityOptions,
	deadlines *runDeadlines,
) (*preparedToolTurn, *RunOutput, error) {
	turn, err := r.prepareToolTurnExecution(wfCtx.Context(), input, base, st, turnID, parentTracker, ctrl, toolOpts, deadlines)
	if errors.Is(err, errToolCallBatchCapExceeded) {
		out, finalizeErr := r.finalizeWithPlanner(
			wfCtx, reg, input, base, st.ToolEvents, st.AggUsage, st.Caps, st.NextAttempt, turnID, plannerResultNotes(st.Result), planner.TerminationReasonToolCap, deadlines.Hard,
		)
		return nil, out, finalizeErr
	}
	return turn, nil, err
}

func (r *Runtime) executeAndAdvanceToolTurn(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *workflowPlanInput,
	st *runLoopState,
	resumeOpts engine.ActivityOptions,
	toolOpts engine.ActivityOptions,
	deadlines *runDeadlines,
	turnID string,
	parentTracker *childTracker,
	ctrl *interrupt.Controller,
	result *planner.PlanResult,
	turn *preparedToolTurn,
) (*RunOutput, error) {
	outcomes, timedOut, err := r.executePreparedToolTurn(wfCtx, reg, input, base, result.ExpectedChildren, parentTracker, turn, toolOpts)
	if err != nil {
		return nil, err
	}
	vals := toolResultsFromExecutions(outcomes)
	toolPauses := toolPausesFromExecutions(outcomes)
	if err := applyExecutedToolTurn(wfCtx.Context(), r, base, st, turn.toExecute, vals); err != nil {
		return nil, err
	}
	if out, err := r.finishOrContinueToolTurn(wfCtx, reg, input, base, st, resumeOpts, toolOpts, deadlines, turnID, parentTracker, ctrl, turn, vals, toolPauses, timedOut); err != nil || out != nil {
		return out, err
	}
	if len(turn.confirmations) == 0 && result.Await == nil && len(toolPauses) == 0 &&
		r.allSuccessfulBookkeepingResults(vals) {
		if result.FinalResponse != nil || result.FinalToolResult != nil {
			return r.finishWithoutToolCalls(wfCtx.Context(), input, base, st, turnID)
		}
		return nil, errors.New("successful bookkeeping-only tool turn must also complete or await")
	}
	return r.resumeAfterToolTurn(wfCtx, reg, input, base, st, resumeOpts, deadlines, turnID)
}

func (r *Runtime) finishOrContinueToolTurn(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *workflowPlanInput,
	st *runLoopState,
	resumeOpts engine.ActivityOptions,
	toolOpts engine.ActivityOptions,
	deadlines *runDeadlines,
	turnID string,
	parentTracker *childTracker,
	ctrl *interrupt.Controller,
	turn *preparedToolTurn,
	vals []*planner.ToolResult,
	toolPauses []*ToolPause,
	timedOut bool,
) (*RunOutput, error) {
	completion := completionTool(input)
	completionSucceeded := completionToolSucceeded(vals, completion)
	terminal, err := r.executedTerminalRunTool(vals)
	if err != nil {
		return nil, err
	}
	if len(toolPauses) > 0 {
		switch {
		case completionSucceeded:
			return nil, fmt.Errorf("completion tool %q must not request a pause", completion)
		case terminal:
			return nil, errors.New("terminal tool must not request a pause")
		}
	}
	if completionSucceeded {
		return r.finishAfterCompletionTool(wfCtx.Context(), input, base, st, turnID, completion)
	}
	if timedOut {
		return r.finalizeWithPlanner(wfCtx, reg, input, base, st.ToolEvents, st.AggUsage, st.Caps, st.NextAttempt, turnID, plannerResultNotes(st.Result), planner.TerminationReasonTimeBudget, deadlines.Hard)
	}
	if terminal {
		return r.finishAfterTerminalToolCalls(wfCtx.Context(), input, base, st, turnID)
	}
	return r.handleToolTurnPostExecution(
		wfCtx, reg, input, base, st, resumeOpts, toolOpts, deadlines, turnID, parentTracker, ctrl, turn.confirmations, vals, turn.allowed, toolPauses,
	)
}

// toolResultsFromExecutions extracts durable planner-visible tool results from a
// batch of runtime-owned execution outcomes.
func toolResultsFromExecutions(outcomes []*ToolExecutionResult) []*planner.ToolResult {
	if len(outcomes) == 0 {
		return nil
	}
	results := make([]*planner.ToolResult, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome == nil || outcome.ToolResult == nil {
			continue
		}
		results = append(results, outcome.ToolResult)
	}
	return results
}

// toolPausesFromExecutions extracts current-batch runtime pause signals from a
// batch of execution outcomes in canonical tool-call order.
func toolPausesFromExecutions(outcomes []*ToolExecutionResult) []*ToolPause {
	if len(outcomes) == 0 {
		return nil
	}
	pauses := make([]*ToolPause, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome == nil || outcome.Pause == nil {
			continue
		}
		pauses = append(pauses, outcome.Pause)
	}
	return pauses
}

// toolPauseAwaitItems projects runtime-owned tool pauses into the existing await
// queue item model.
func toolPauseAwaitItems(pauses []*ToolPause) ([]planner.AwaitItem, error) {
	if len(pauses) == 0 {
		return nil, nil
	}
	items := make([]planner.AwaitItem, 0, len(pauses))
	for i, pause := range pauses {
		if pause == nil || pause.Clarification == nil {
			return nil, fmt.Errorf("tool pause %d is invalid", i)
		}
		items = append(items, planner.AwaitClarificationItem(&planner.AwaitClarification{
			ID:       pause.Clarification.ID,
			Question: pause.Clarification.Question,
		}))
	}
	return items, nil
}

type preparedToolTurn struct {
	allowed       []planner.ToolRequest
	toExecute     []planner.ToolRequest
	confirmations []confirmationAwait
	grouped       [][]planner.ToolRequest
	timeouts      []time.Duration
	finishBy      time.Time
}

func (r *Runtime) prepareToolTurnExecution(
	ctx context.Context,
	input *RunInput,
	base *workflowPlanInput,
	st *runLoopState,
	turnID string,
	parentTracker *childTracker,
	ctrl *interrupt.Controller,
	toolOpts engine.ActivityOptions,
	deadlines *runDeadlines,
) (*preparedToolTurn, error) {
	allowed, toExecute, confirmations, execCalls, err := r.prepareToolTurnCalls(ctx, input, base, st, turnID, parentTracker, ctrl)
	if err != nil {
		return nil, err
	}
	grouped, timeouts := r.groupToolCallsByTimeout(execCalls, input, toolOpts.StartToCloseTimeout)
	return &preparedToolTurn{
		allowed:       allowed,
		toExecute:     toExecute,
		confirmations: confirmations,
		grouped:       grouped,
		timeouts:      timeouts,
		finishBy:      toolTurnFinishBy(deadlines),
	}, nil
}

func toolTurnFinishBy(deadlines *runDeadlines) time.Time {
	if deadlines == nil || deadlines.Hard.IsZero() {
		return time.Time{}
	}
	return deadlines.Hard.Add(-deadlines.finalizeReserve())
}

func (r *Runtime) executePreparedToolTurn(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *workflowPlanInput,
	expectedChildren int,
	parentTracker *childTracker,
	turn *preparedToolTurn,
	toolOpts engine.ActivityOptions,
) ([]*ToolExecutionResult, bool, error) {
	return r.executeGroupedToolCalls(
		wfCtx,
		reg,
		input.AgentID,
		base,
		expectedChildren,
		parentTracker,
		turn.finishBy,
		turn.grouped,
		turn.timeouts,
		toolOpts,
	)
}

func applyExecutedToolTurn(
	ctx context.Context,
	r *Runtime,
	base *workflowPlanInput,
	st *runLoopState,
	toExecute []planner.ToolRequest,
	vals []*planner.ToolResult,
) error {
	if err := r.appendIndexResults(ctx, base, toExecute, vals, false); err != nil {
		return err
	}
	st.ToolEvents = append(st.ToolEvents, cloneToolResults(vals)...)
	return nil
}

func (r *Runtime) resumeAfterToolTurn(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *workflowPlanInput,
	st *runLoopState,
	resumeOpts engine.ActivityOptions,
	deadlines *runDeadlines,
	turnID string,
) (*RunOutput, error) {
	if resultsContainOutcomeUnknown(st.ToolEvents) {
		return r.finalizeWithPlanner(
			wfCtx, reg, input, base, st.ToolEvents, st.AggUsage, st.Caps,
			st.NextAttempt, turnID, plannerResultNotes(st.Result),
			planner.TerminationReasonOutcomeUnknown, deadlines.Hard,
		)
	}
	policyResult, err := r.preparePrePlanToolPolicy(wfCtx.Context(), reg, input, base, st.Caps, turnID)
	if err != nil {
		return nil, err
	}
	st.Caps = policyResult.Caps
	resumeReq, err := r.buildNextResumeRequest(input.AgentID, base, &st.NextAttempt)
	if err != nil {
		return nil, err
	}
	resumeReq.CallFilter = planToolFilter(input)
	resumeReq.ToolPolicyActive = policyResult.Envelope.Active
	resumeReq.AllowedTools = cloneToolIdents(policyResult.Envelope.Allowed)
	resumeReq.PolicyCaps = st.Caps
	resumeReq.RunContext.Labels = cloneLabels(base.RunContext.Labels)
	resOutput, err := r.runPlanActivityRecovering(wfCtx, reg.ResumeActivityName, resumeOpts, resumeReq, deadlines.Budget, &st.Caps, &st.NextAttempt)
	if err != nil {
		return r.handleResumePlanError(wfCtx, reg, input, base, st, resOutput, deadlines, turnID, err)
	}
	base.adopt(resOutput)
	return applyResumePlanOutput(st, resOutput)
}

func (r *Runtime) handleResumePlanError(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *workflowPlanInput,
	st *runLoopState,
	resOutput *PlanActivityOutput,
	deadlines *runDeadlines,
	turnID string,
	resumeErr error,
) (*RunOutput, error) {
	if deadlines.budgetExpired(wfCtx.Now()) {
		return r.finalizeAfterResumePlanError(
			wfCtx,
			reg,
			input,
			base,
			st,
			resOutput,
			deadlines,
			turnID,
			planner.TerminationReasonTimeBudget,
		)
	}
	if !errors.Is(resumeErr, errRecoveryTurnCapExceeded) || resOutput == nil {
		return nil, resumeErr
	}
	return r.finalizeAfterResumePlanError(
		wfCtx,
		reg,
		input,
		base,
		st,
		resOutput,
		deadlines,
		turnID,
		planner.TerminationReasonFailureCap,
	)
}

func (r *Runtime) finalizeAfterResumePlanError(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *workflowPlanInput,
	st *runLoopState,
	resOutput *PlanActivityOutput,
	deadlines *runDeadlines,
	turnID string,
	reason planner.TerminationReason,
) (*RunOutput, error) {
	if resOutput != nil {
		usage, err := checkedAddTokenUsage(st.AggUsage, resOutput.Usage)
		if err != nil {
			return nil, fmt.Errorf("aggregate resumed model usage: %w", err)
		}
		st.AggUsage = usage
	}
	return r.finalizeWithPlanner(
		wfCtx,
		reg,
		input,
		base,
		st.ToolEvents,
		st.AggUsage,
		st.Caps,
		st.NextAttempt,
		turnID,
		plannerResultNotes(st.Result),
		reason,
		deadlines.Hard,
	)
}

func applyResumePlanOutput(st *runLoopState, resOutput *PlanActivityOutput) (*RunOutput, error) {
	if resOutput == nil || resOutput.Result == nil {
		return nil, fmt.Errorf("plan activity returned nil result on resume")
	}
	usage, err := checkedAddTokenUsage(st.AggUsage, resOutput.Usage)
	if err != nil {
		return nil, fmt.Errorf("aggregate resumed model usage: %w", err)
	}
	st.AggUsage = usage
	st.Result = resOutput.Result
	st.setTurnTranscript(resOutput.TranscriptRef)
	st.ToolPolicy = toolPolicyEnvelope{
		Active:  resOutput.ToolPolicyActive,
		Allowed: cloneToolIdents(resOutput.AllowedTools),
	}
	return nil, nil
}

func (r *Runtime) enforceToolTurnGuards(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *workflowPlanInput,
	st *runLoopState,
	turnID string,
	deadlines *runDeadlines,
) (*RunOutput, error) {
	if st.Caps.RemainingToolCalls == 0 && st.Caps.MaxToolCalls > 0 &&
		r.budgetedToolCallCount(st.Result.ToolCalls) > 0 {
		return r.finalizeWithPlanner(wfCtx, reg, input, base, st.ToolEvents, st.AggUsage, st.Caps, st.NextAttempt, turnID, plannerResultNotes(st.Result), planner.TerminationReasonToolCap, deadlines.Hard)
	}

	if deadlines.budgetExpired(wfCtx.Now()) {
		return r.finalizeWithPlanner(wfCtx, reg, input, base, st.ToolEvents, st.AggUsage, st.Caps, st.NextAttempt, turnID, plannerResultNotes(st.Result), planner.TerminationReasonTimeBudget, deadlines.Hard)
	}
	return nil, nil
}

func (r *Runtime) prepareToolTurnCalls(
	ctx context.Context,
	input *RunInput,
	base *workflowPlanInput,
	st *runLoopState,
	turnID string,
	parentTracker *childTracker,
	ctrl *interrupt.Controller,
) ([]planner.ToolRequest, []planner.ToolRequest, []confirmationAwait, []planner.ToolRequest, error) {
	allowed, err := r.allowedToolTurnCalls(ctx, input, base, st, turnID)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	r.logger.Info(ctx, "Executing allowed tool calls", "count", len(allowed))
	if err := r.updateParentTracker(ctx, base, turnID, parentTracker, allowed); err != nil {
		return nil, nil, nil, nil, err
	}

	toExecute, confirmations, err := r.splitConfirmationCalls(ctx, base, allowed)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if len(confirmations) > 0 && ctrl == nil {
		return nil, nil, nil, nil, fmt.Errorf("confirmation required but interrupts are not available")
	}
	if len(toExecute) > 0 {
		if err := base.recordAssistant(st.takeTurnTranscript(), toExecute); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	return allowed, toExecute, confirmations, toExecute, nil
}

func (r *Runtime) allowedToolTurnCalls(
	ctx context.Context,
	input *RunInput,
	base *workflowPlanInput,
	st *runLoopState,
	turnID string,
) ([]planner.ToolRequest, error) {
	candidates := st.Result.ToolCalls
	r.logger.Info(ctx, "Workflow received tool calls from planner", "count", len(candidates))
	candidates = r.applyPerRunOverrides(ctx, input, candidates)
	if st.ToolPolicy.Active && len(st.ToolPolicy.Allowed) == 0 {
		return nil, errors.New("no tools allowed for execution")
	}
	result, err := r.applyPolicy(ctx, base, input, candidates, st.Caps, turnID, st.Result.RetryHint, st.ToolPolicy)
	if err != nil {
		return nil, err
	}
	st.Caps = result.Caps
	if result.CapExceeded {
		return nil, errToolCallBatchCapExceeded
	}
	if len(result.AllowedCalls) == 0 {
		r.logger.Error(ctx, "ERROR - No tools allowed for execution after filtering", "candidates", len(st.Result.ToolCalls))
		return nil, errors.New("no tools allowed for execution")
	}
	return result.AllowedCalls, nil
}

func (r *Runtime) updateParentTracker(ctx context.Context, base *workflowPlanInput, turnID string, parentTracker *childTracker, allowed []planner.ToolRequest) error {
	if parentTracker == nil {
		return nil
	}
	ids := collectToolCallIDs(allowed)
	if len(ids) == 0 || !parentTracker.registerDiscovered(ids) {
		return nil
	}
	if base.RunContext.ParentRunID == "" || base.RunContext.ParentAgentID == "" {
		return fmt.Errorf("nested run is missing parent run context")
	}
	if err := r.publishHook(
		ctx,
		hooks.NewToolCallUpdatedEvent(
			base.RunContext.ParentRunID,
			base.RunContext.ParentAgentID,
			base.RunContext.SessionID,
			parentTracker.parentToolCallID,
			parentTracker.currentTotal(),
		),
		turnID,
	); err != nil {
		return err
	}
	parentTracker.markUpdated()
	return nil
}

func (r *Runtime) handleToolTurnPostExecution(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *workflowPlanInput,
	st *runLoopState,
	resumeOpts engine.ActivityOptions,
	toolOpts engine.ActivityOptions,
	deadlines *runDeadlines,
	turnID string,
	parentTracker *childTracker,
	ctrl *interrupt.Controller,
	confirmations []confirmationAwait,
	lastToolResults []*planner.ToolResult,
	allowed []planner.ToolRequest,
	toolPauses []*ToolPause,
) (*RunOutput, error) {
	st.Caps.RemainingToolCalls = decrementCap(st.Caps.RemainingToolCalls, r.budgetedToolCallCount(allowed))

	if st.Result.Await != nil && len(st.Result.Await.Items) > 0 && len(toolPauses) > 0 {
		return nil, fmt.Errorf("planner await and tool pause cannot both be present in the same turn")
	}
	if len(confirmations) > 0 || (st.Result.Await != nil && len(st.Result.Await.Items) > 0) || len(toolPauses) > 0 {
		items := make([]planner.AwaitItem, 0)
		if st.Result.Await != nil {
			items = append(items, st.Result.Await.Items...)
		}
		if len(toolPauses) > 0 {
			pauseItems, err := toolPauseAwaitItems(toolPauses)
			if err != nil {
				return nil, err
			}
			items = append(items, pauseItems...)
		}
		return r.handleAwaitQueue(
			wfCtx, reg, input, base, st, resumeOpts, toolOpts, st.Result.ExpectedChildren, parentTracker, ctrl, deadlines, turnID, confirmations, items, lastToolResults,
		)
	}
	if out, err := r.applyFailureAndProtectionPolicy(wfCtx, reg, input, base, st, turnID, ctrl, deadlines, lastToolResults); err != nil || out != nil {
		return out, err
	}
	return nil, nil
}

func (r *Runtime) applyFailureAndProtectionPolicy(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *workflowPlanInput,
	st *runLoopState,
	turnID string,
	ctrl *interrupt.Controller,
	deadlines *runDeadlines,
	vals []*planner.ToolResult,
) (*RunOutput, error) {
	if resultsContainOutcomeUnknown(vals) {
		return r.finalizeWithPlanner(
			wfCtx, reg, input, base, st.ToolEvents, st.AggUsage, st.Caps,
			st.NextAttempt, turnID, plannerResultNotes(st.Result),
			planner.TerminationReasonOutcomeUnknown, deadlines.Hard,
		)
	}
	resetRecoveryTurnsAfterResults(r, &st.Caps, vals)
	if resultsRequireRecovery(vals) {
		if !consumeRecoveryTurn(&st.Caps) {
			return r.finalizeWithPlanner(wfCtx, reg, input, base, st.ToolEvents, st.AggUsage, st.Caps, st.NextAttempt, turnID, plannerResultNotes(st.Result), planner.TerminationReasonFailureCap, deadlines.Hard)
		}
	}
	if out, err := r.handleMissingFieldsPolicy(wfCtx, reg, input, base, vals, st.ToolEvents, st.AggUsage, st.Caps, &st.NextAttempt, turnID, plannerResultNotes(st.Result), ctrl, deadlines); err != nil || out != nil {
		return out, err
	}
	protected, err := r.hardProtectionIfNeeded(wfCtx.Context(), input.AgentID, base, vals, turnID)
	if err != nil {
		return nil, err
	}
	if protected {
		return r.finalizeWithPlanner(wfCtx, reg, input, base, st.ToolEvents, st.AggUsage, st.Caps, st.NextAttempt, turnID, plannerResultNotes(st.Result), planner.TerminationReasonFailureCap, deadlines.Hard)
	}
	return nil, nil
}

func (r *Runtime) allSuccessfulBookkeepingResults(results []*planner.ToolResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, result := range results {
		if result == nil || result.Error != nil || !r.isBookkeeping(result.Name) {
			return false
		}
	}
	return true
}

func (r *Runtime) executedTerminalRunTool(results []*planner.ToolResult) (bool, error) {
	executed := false
	for _, tr := range results {
		if tr == nil {
			continue
		}
		spec, ok := r.toolSpec(tr.Name)
		if !ok {
			return false, fmt.Errorf("unknown tool %q", tr.Name)
		}
		if !spec.TerminalRun {
			continue
		}
		if tr.Error != nil {
			return false, fmt.Errorf("terminal tool %q failed: %s", tr.Name, tr.Error.Message)
		}
		executed = true
	}
	return executed, nil
}
