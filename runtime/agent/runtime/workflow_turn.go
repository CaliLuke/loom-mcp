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

	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/transcript"
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
	base *planner.PlanInput,
	st *runLoopState,
	resumeOpts engine.ActivityOptions,
	toolOpts engine.ActivityOptions,
	deadlines *runDeadlines,
	turnID string,
	parentTracker *childTracker,
	ctrl *interrupt.Controller,
) (*RunOutput, error) {
	ctx := wfCtx.Context()
	result := st.Result
	if deadlines == nil {
		return nil, errors.New("missing run deadlines")
	}
	if out, err := r.enforceToolTurnGuards(wfCtx, reg, input, base, st, turnID, deadlines); err != nil || out != nil {
		return out, err
	}

	allowed, toExecute, confirmations, execCalls, err := r.prepareToolTurnCalls(ctx, input, base, st, turnID, parentTracker, ctrl)
	if err != nil {
		return nil, err
	}

	grouped, timeouts := r.groupToolCallsByTimeout(execCalls, input, toolOpts.StartToCloseTimeout)
	finishBy := time.Time{}
	if !deadlines.Hard.IsZero() {
		finishBy = deadlines.Hard.Add(-deadlines.finalizeReserve())
	}
	vals, timedOut, err := r.executeGroupedToolCalls(wfCtx, reg, input.AgentID, base, result.ExpectedChildren, parentTracker, finishBy, grouped, timeouts, toolOpts)
	if err != nil {
		return nil, err
	}
	lastToolResults := vals
	st.ToolEvents = append(st.ToolEvents, cloneToolResults(vals)...)
	if err := r.appendToolOutputs(ctx, st, toExecute, vals); err != nil {
		return nil, err
	}
	if err := r.appendUserToolResults(base, toExecute, vals, st.Ledger); err != nil {
		return nil, err
	}
	if timedOut {
		out, err := r.finalizeWithPlanner(wfCtx, reg, input, base, st.ToolEvents, st.ToolOutputs, st.AggUsage, st.NextAttempt, turnID, planner.TerminationReasonTimeBudget, deadlines.Hard)
		return out, err
	}

	terminal, err := r.executedTerminalRunTool(vals)
	if err != nil {
		return nil, err
	}
	if terminal {
		return r.finishAfterTerminalToolCalls(ctx, input, base, st)
	}
	if out, err := r.handleToolTurnPostExecution(wfCtx, reg, input, base, st, resumeOpts, toolOpts, deadlines, turnID, parentTracker, ctrl, confirmations, lastToolResults, allowed, vals); err != nil || out != nil {
		return out, err
	}

	resumeReq, err := r.buildNextResumeRequest(input.AgentID, base, st.ToolOutputs, &st.NextAttempt)
	if err != nil {
		return nil, err
	}
	resOutput, err := r.runPlanActivity(wfCtx, reg.ResumeActivityName, resumeOpts, resumeReq, deadlines.Budget)
	if err != nil {
		return nil, err
	}
	if resOutput == nil || resOutput.Result == nil {
		return nil, fmt.Errorf("plan activity returned nil result on resume")
	}
	st.AggUsage = addTokenUsage(st.AggUsage, resOutput.Usage)
	st.Result = resOutput.Result
	st.Transcript = resOutput.Transcript
	st.Ledger = transcript.FromModelMessages(st.Transcript)
	return nil, nil
}

func (r *Runtime) enforceToolTurnGuards(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
	st *runLoopState,
	turnID string,
	deadlines *runDeadlines,
) (*RunOutput, error) {
	if st.Caps.RemainingToolCalls == 0 && st.Caps.MaxToolCalls > 0 {
		return r.finalizeWithPlanner(wfCtx, reg, input, base, st.ToolEvents, st.ToolOutputs, st.AggUsage, st.NextAttempt, turnID, planner.TerminationReasonToolCap, deadlines.Hard)
	}
	if !deadlines.Budget.IsZero() && wfCtx.Now().After(deadlines.Budget) {
		return r.finalizeWithPlanner(wfCtx, reg, input, base, st.ToolEvents, st.ToolOutputs, st.AggUsage, st.NextAttempt, turnID, planner.TerminationReasonTimeBudget, deadlines.Hard)
	}
	return nil, nil
}

func (r *Runtime) prepareToolTurnCalls(
	ctx context.Context,
	input *RunInput,
	base *planner.PlanInput,
	st *runLoopState,
	turnID string,
	parentTracker *childTracker,
	ctrl *interrupt.Controller,
) ([]planner.ToolRequest, []planner.ToolRequest, []confirmationAwait, []planner.ToolRequest, error) {
	candidates := st.Result.ToolCalls
	r.logger.Info(ctx, "Workflow received tool calls from planner", "count", len(candidates))
	candidates = r.applyPerRunOverrides(ctx, input, candidates)
	rewritten, err := r.rewriteUnknownToolCalls(candidates)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	allowed, nextCaps, err := r.applyRuntimePolicy(ctx, base, input, rewritten, st.Caps, turnID, st.Result.RetryHint)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	st.Caps = nextCaps
	if len(allowed) == 0 {
		r.logger.Error(ctx, "ERROR - No tools allowed for execution after filtering", "candidates", len(st.Result.ToolCalls))
		return nil, nil, nil, nil, errors.New("no tools allowed for execution")
	}
	r.logger.Info(ctx, "Executing allowed tool calls", "count", len(allowed))
	if err := r.updateParentTracker(ctx, base, turnID, parentTracker, allowed); err != nil {
		return nil, nil, nil, nil, err
	}
	allowed = r.capAllowedCalls(allowed, input, st.Caps)
	allowed = r.prepareAllowedCallsMetadata(input.AgentID, base, allowed, parentTracker)
	toExecute, confirmations, err := r.splitConfirmationCalls(ctx, base, allowed)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if len(confirmations) > 0 && ctrl == nil {
		return nil, nil, nil, nil, fmt.Errorf("confirmation required but interrupts are not available")
	}
	if len(toExecute) > 0 {
		r.recordAssistantTurn(base, st.Transcript, toExecute, st.Ledger)
	}
	return allowed, toExecute, confirmations, ensureToolCallIDs(base, toExecute), nil
}

func (r *Runtime) updateParentTracker(ctx context.Context, base *planner.PlanInput, turnID string, parentTracker *childTracker, allowed []planner.ToolRequest) error {
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

func ensureToolCallIDs(base *planner.PlanInput, calls []planner.ToolRequest) []planner.ToolRequest {
	out := make([]planner.ToolRequest, len(calls))
	for i := range calls {
		call := calls[i]
		if call.ToolCallID == "" {
			call.ToolCallID = generateDeterministicToolCallID(base.RunContext.RunID, call.TurnID, base.RunContext.Attempt, call.Name, i)
		}
		out[i] = call
	}
	return out
}

func (r *Runtime) handleToolTurnPostExecution(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
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
	vals []*planner.ToolResult,
) (*RunOutput, error) {
	st.Caps.RemainingToolCalls = decrementCap(st.Caps.RemainingToolCalls, len(allowed))
	if len(confirmations) > 0 || (st.Result.Await != nil && len(st.Result.Await.Items) > 0) {
		items := []planner.AwaitItem(nil)
		if st.Result.Await != nil {
			items = st.Result.Await.Items
		}
		return r.handleAwaitQueue(
			wfCtx, reg, input, base, st, resumeOpts, toolOpts, st.Result.ExpectedChildren, parentTracker, ctrl, deadlines, turnID, confirmations, items, lastToolResults,
		)
	}
	if out, err := r.applyFailureAndProtectionPolicy(wfCtx, reg, input, base, st, turnID, ctrl, deadlines, vals); err != nil || out != nil {
		return out, err
	}
	return nil, nil
}

func (r *Runtime) applyFailureAndProtectionPolicy(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
	st *runLoopState,
	turnID string,
	ctrl *interrupt.Controller,
	deadlines *runDeadlines,
	vals []*planner.ToolResult,
) (*RunOutput, error) {
	if failures := capFailures(vals); failures > 0 {
		st.Caps.RemainingConsecutiveFailedToolCalls = decrementCap(st.Caps.RemainingConsecutiveFailedToolCalls, failures)
		if st.Caps.MaxConsecutiveFailedToolCalls > 0 && st.Caps.RemainingConsecutiveFailedToolCalls <= 0 {
			return r.finalizeWithPlanner(wfCtx, reg, input, base, st.ToolEvents, st.ToolOutputs, st.AggUsage, st.NextAttempt, turnID, planner.TerminationReasonFailureCap, deadlines.Hard)
		}
	} else if st.Caps.MaxConsecutiveFailedToolCalls > 0 {
		st.Caps.RemainingConsecutiveFailedToolCalls = st.Caps.MaxConsecutiveFailedToolCalls
	}
	if out, err := r.handleMissingFieldsPolicy(wfCtx, reg, input, base, vals, st.ToolEvents, st.ToolOutputs, st.AggUsage, &st.NextAttempt, turnID, ctrl, deadlines); err != nil || out != nil {
		return out, err
	}
	protected, err := r.hardProtectionIfNeeded(wfCtx.Context(), input.AgentID, base, vals, turnID)
	if err != nil {
		return nil, err
	}
	if protected {
		return r.finalizeWithPlanner(wfCtx, reg, input, base, st.ToolEvents, st.ToolOutputs, st.AggUsage, st.NextAttempt, turnID, planner.TerminationReasonFailureCap, deadlines.Hard)
	}
	return nil, nil
}

func (r *Runtime) executedTerminalRunTool(results []*planner.ToolResult) (bool, error) {
	for _, tr := range results {
		if tr == nil {
			continue
		}
		spec, ok := r.toolSpec(tr.Name)
		if !ok {
			return false, fmt.Errorf("unknown tool %q", tr.Name)
		}
		if spec.TerminalRun {
			return true, nil
		}
	}
	return false, nil
}
