package runtime

// workflow_await_queue.go contains workflow-side support for queued await
// prompts returned by planners.
//
// Contract:
// - Planners may return an Await barrier containing multiple ordered await
//   items (clarifications, questions, external tool handshakes).
// - The runtime publishes all await events, pauses once, then waits for each
//   item to be satisfied in order.
// - The runtime resumes planning exactly once after the entire await queue is
//   satisfied, so planners observe all user/external inputs together.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/transcript"
)

const awaitReasonQueue = "await_queue"

func (r *Runtime) waitAwaitConfirmation(
	ctx context.Context,
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
	st *runLoopState,
	toolOpts engine.ActivityOptions,
	expectedChildren int,
	parentTracker *childTracker,
	turnID string,
	ctrl *interrupt.Controller,
	deadlines *runDeadlines,
	it confirmationAwait,
) ([]*planner.ToolResult, *RunOutput, error) {
	if deadlines == nil {
		return nil, nil, errors.New("missing run deadlines")
	}
	dec, err := waitForConfirmationDecision(ctx, wfCtx, ctrl, deadlines)
	if err != nil {
		return nil, nil, err
	}
	if err := validateConfirmationDecision(dec, it); err != nil {
		return nil, nil, err
	}
	if err := r.publishAuthorizationDecision(ctx, input, base, turnID, it, dec); err != nil {
		return nil, nil, err
	}

	// Confirmation gates tool execution. We represent both approval and denial as
	// a provider-visible tool_use + tool_result pair so planners see a deterministic
	// outcome for the tool call they requested.
	r.recordAssistantTurn(base, st.Transcript, []planner.ToolRequest{it.call}, st.Ledger)

	if !dec.Approved {
		return r.handleDeniedConfirmation(ctx, base, st, turnID, expectedChildren, it)
	}
	return r.executeConfirmedToolCall(ctx, wfCtx, reg, input, base, st, toolOpts, expectedChildren, parentTracker, turnID, deadlines, it)
}

func (r *Runtime) handleAwaitQueue(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
	st *runLoopState,
	resumeOpts engine.ActivityOptions,
	toolOpts engine.ActivityOptions,
	expectedChildren int,
	parentTracker *childTracker,
	ctrl *interrupt.Controller,
	deadlines *runDeadlines,
	turnID string,
	confirmations []confirmationAwait,
	items []planner.AwaitItem,
	priorToolResults []*planner.ToolResult,
) (*RunOutput, error) {
	ctx := wfCtx.Context()
	if err := validateAwaitQueueInputs(ctrl, deadlines, confirmations, items); err != nil {
		return nil, err
	}
	if err := r.publishAwaitPrompts(ctx, input, base, st, turnID, confirmations, items); err != nil {
		return nil, err
	}
	if err := r.publishAwaitPause(ctx, input, base, turnID); err != nil {
		return nil, err
	}
	allToolResults, out, err := r.collectAwaitResults(ctx, wfCtx, reg, input, base, st, toolOpts, expectedChildren, parentTracker, turnID, ctrl, deadlines, confirmations, items, priorToolResults)
	if err != nil {
		return nil, err
	}
	if out != nil {
		return out, nil
	}
	if out, err := r.handleAwaitPostProcessing(ctx, wfCtx, reg, input, base, st, resumeOpts, turnID, ctrl, deadlines, confirmations, items, allToolResults); err != nil {
		return nil, err
	} else if out != nil {
		return out, nil
	}
	return nil, nil
}

func validateAwaitQueueInputs(ctrl *interrupt.Controller, deadlines *runDeadlines, confirmations []confirmationAwait, items []planner.AwaitItem) error {
	if ctrl == nil {
		return errors.New("await not supported in inline runs")
	}
	if deadlines == nil {
		return errors.New("missing run deadlines")
	}
	if len(confirmations) == 0 && len(items) == 0 {
		return errors.New("await: empty await queue")
	}
	return nil
}

func (r *Runtime) publishAwaitPrompts(ctx context.Context, input *RunInput, base *planner.PlanInput, st *runLoopState, turnID string, confirmations []confirmationAwait, items []planner.AwaitItem) error {
	if err := r.publishAwaitConfirmations(ctx, input, base, turnID, confirmations); err != nil {
		return err
	}
	for i, it := range items {
		if err := r.publishAwaitQueueItem(ctx, input, base, st, turnID, it, i); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) publishAwaitConfirmations(ctx context.Context, input *RunInput, base *planner.PlanInput, turnID string, confirmations []confirmationAwait) error {
	for i, it := range confirmations {
		if it.plan == nil {
			return fmt.Errorf("await confirmation item %d missing plan", i)
		}
		title := it.plan.Title
		if title == "" {
			title = "Confirm command"
		}
		if err := r.publishHook(ctx, hooks.NewAwaitConfirmationEvent(
			base.RunContext.RunID,
			input.AgentID,
			base.RunContext.SessionID,
			it.awaitID,
			title,
			it.plan.Prompt,
			it.call.Name,
			it.call.ToolCallID,
			it.call.Payload,
		), turnID); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) publishAwaitPause(ctx context.Context, input *RunInput, base *planner.PlanInput, turnID string) error {
	return r.publishHook(
		ctx,
		hooks.NewRunPausedEvent(base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, awaitReasonQueue, "runtime", nil, nil),
		turnID,
	)
}

func (r *Runtime) collectAwaitResults(ctx context.Context, wfCtx engine.WorkflowContext, reg AgentRegistration, input *RunInput, base *planner.PlanInput, st *runLoopState, toolOpts engine.ActivityOptions, expectedChildren int, parentTracker *childTracker, turnID string, ctrl *interrupt.Controller, deadlines *runDeadlines, confirmations []confirmationAwait, items []planner.AwaitItem, priorToolResults []*planner.ToolResult) ([]*planner.ToolResult, *RunOutput, error) {
	waitTimeout := time.Duration(0)
	allToolResults := append(make([]*planner.ToolResult, 0, len(priorToolResults)+8), priorToolResults...)

	for _, it := range confirmations {
		res, out, err := r.waitAwaitConfirmation(ctx, wfCtx, reg, input, base, st, toolOpts, expectedChildren, parentTracker, turnID, ctrl, deadlines, it)
		if err != nil {
			return nil, nil, err
		}
		if out != nil {
			return nil, out, nil
		}
		allToolResults = appendAwaitToolResults(allToolResults, res)
	}
	for _, it := range items {
		res, err := r.waitForAwaitItem(ctx, wfCtx, ctrl, input, base, st, turnID, waitTimeout, deadlines, it)
		if err != nil {
			return nil, nil, err
		}
		allToolResults = appendAwaitToolResults(allToolResults, res)
	}
	return allToolResults, nil, nil
}

func (r *Runtime) waitForAwaitItem(ctx context.Context, wfCtx engine.WorkflowContext, ctrl *interrupt.Controller, input *RunInput, base *planner.PlanInput, st *runLoopState, turnID string, timeout time.Duration, deadlines *runDeadlines, it planner.AwaitItem) ([]*planner.ToolResult, error) {
	waitStartedAt := wfCtx.Now()
	res, err := r.waitAwaitQueueItem(ctx, ctrl, input, base, st, turnID, timeout, it)
	deadlines.pause(wfCtx.Now().Sub(waitStartedAt))
	return res, err
}

func appendAwaitToolResults(current []*planner.ToolResult, extra []*planner.ToolResult) []*planner.ToolResult {
	if len(extra) == 0 {
		return current
	}
	return append(current, extra...)
}

func (r *Runtime) handleAwaitPostProcessing(ctx context.Context, wfCtx engine.WorkflowContext, reg AgentRegistration, input *RunInput, base *planner.PlanInput, st *runLoopState, resumeOpts engine.ActivityOptions, turnID string, ctrl *interrupt.Controller, deadlines *runDeadlines, confirmations []confirmationAwait, items []planner.AwaitItem, allToolResults []*planner.ToolResult) (*RunOutput, error) {
	if out, err := r.applyAwaitFailurePolicy(wfCtx, reg, input, base, st, allToolResults, turnID, ctrl, deadlines); err != nil || out != nil {
		return out, err
	}
	if out, err := r.finalizeProtectedAwaitRun(ctx, wfCtx, reg, input, base, st, turnID, deadlines, allToolResults); err != nil || out != nil {
		return out, err
	}
	if err := r.publishAwaitResume(ctx, input, base, turnID, confirmations, items); err != nil {
		return nil, err
	}
	return r.resumeAfterAwait(wfCtx, reg, input, base, st, resumeOpts, deadlines)
}

func (r *Runtime) finalizeProtectedAwaitRun(ctx context.Context, wfCtx engine.WorkflowContext, reg AgentRegistration, input *RunInput, base *planner.PlanInput, st *runLoopState, turnID string, deadlines *runDeadlines, allToolResults []*planner.ToolResult) (*RunOutput, error) {
	protected, err := r.hardProtectionIfNeeded(ctx, input.AgentID, base, allToolResults, turnID)
	if err != nil {
		return nil, err
	}
	if !protected {
		return nil, nil
	}
	return r.finalizeWithPlanner(wfCtx, reg, input, base, st.ToolEvents, st.ToolOutputs, st.AggUsage, st.NextAttempt, turnID, planner.TerminationReasonFailureCap, deadlines.Hard)
}

func (r *Runtime) resumeAfterAwait(wfCtx engine.WorkflowContext, reg AgentRegistration, input *RunInput, base *planner.PlanInput, st *runLoopState, resumeOpts engine.ActivityOptions, deadlines *runDeadlines) (*RunOutput, error) {
	resumeReq, err := r.buildNextResumeRequest(input.AgentID, base, st.ToolOutputs, &st.NextAttempt)
	if err != nil {
		return nil, err
	}
	resOutput, err := r.runPlanActivity(wfCtx, reg.ResumeActivityName, resumeOpts, resumeReq, deadlines.Budget)
	if err != nil {
		return nil, err
	}
	if resOutput == nil || resOutput.Result == nil {
		return nil, fmt.Errorf("plan resume activity returned nil result after await")
	}
	st.AggUsage = addTokenUsage(st.AggUsage, resOutput.Usage)
	st.Result = resOutput.Result
	st.Transcript = resOutput.Transcript
	st.Ledger = transcript.FromModelMessages(st.Transcript)
	return nil, nil
}

func (r *Runtime) applyAwaitFailurePolicy(wfCtx engine.WorkflowContext, reg AgentRegistration, input *RunInput, base *planner.PlanInput, st *runLoopState, allToolResults []*planner.ToolResult, turnID string, ctrl *interrupt.Controller, deadlines *runDeadlines) (*RunOutput, error) {
	if out, err := r.applyAwaitFailureCap(wfCtx, reg, input, base, st, allToolResults, turnID, deadlines); err != nil || out != nil {
		return out, err
	}
	return r.handleMissingFieldsPolicy(wfCtx, reg, input, base, allToolResults, st.ToolEvents, st.ToolOutputs, st.AggUsage, &st.NextAttempt, turnID, ctrl, deadlines)
}

func (r *Runtime) applyAwaitFailureCap(wfCtx engine.WorkflowContext, reg AgentRegistration, input *RunInput, base *planner.PlanInput, st *runLoopState, allToolResults []*planner.ToolResult, turnID string, deadlines *runDeadlines) (*RunOutput, error) {
	failures := capFailures(allToolResults)
	if failures == 0 {
		if st.Caps.MaxConsecutiveFailedToolCalls > 0 {
			st.Caps.RemainingConsecutiveFailedToolCalls = st.Caps.MaxConsecutiveFailedToolCalls
		}
		return nil, nil
	}
	st.Caps.RemainingConsecutiveFailedToolCalls = decrementCap(st.Caps.RemainingConsecutiveFailedToolCalls, failures)
	if st.Caps.MaxConsecutiveFailedToolCalls == 0 || st.Caps.RemainingConsecutiveFailedToolCalls > 0 {
		return nil, nil
	}
	return r.finalizeWithPlanner(wfCtx, reg, input, base, st.ToolEvents, st.ToolOutputs, st.AggUsage, st.NextAttempt, turnID, planner.TerminationReasonFailureCap, deadlines.Hard)
}

func (r *Runtime) publishAwaitResume(ctx context.Context, input *RunInput, base *planner.PlanInput, turnID string, confirmations []confirmationAwait, items []planner.AwaitItem) error {
	return r.publishHook(
		ctx,
		hooks.NewRunResumedEvent(base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, "await_completed", "runtime", map[string]string{
			"resumed_by":    "await_queue",
			"confirmations": fmt.Sprintf("%d", len(confirmations)),
			"items":         fmt.Sprintf("%d", len(items)),
		}, 0),
		turnID,
	)
}

func waitForConfirmationDecision(ctx context.Context, wfCtx engine.WorkflowContext, ctrl *interrupt.Controller, deadlines *runDeadlines) (interrupt.ConfirmationDecision, error) {
	waitStartedAt := wfCtx.Now()
	dec, err := ctrl.WaitProvideConfirmation(ctx, 0)
	if err != nil {
		return nil, err
	}
	deadlines.pause(wfCtx.Now().Sub(waitStartedAt))
	return dec, nil
}

func validateConfirmationDecision(dec interrupt.ConfirmationDecision, it confirmationAwait) error {
	if dec == nil {
		return errors.New("await_confirmation: received nil confirmation decision")
	}
	if dec.ID != "" && dec.ID != it.awaitID {
		return fmt.Errorf("unexpected confirmation id %q (expected %q)", dec.ID, it.awaitID)
	}
	if dec.RequestedBy == "" {
		return fmt.Errorf("confirmation decision missing requested_by for %q (%s)", it.call.Name, it.call.ToolCallID)
	}
	return nil
}

func (r *Runtime) publishAuthorizationDecision(ctx context.Context, input *RunInput, base *planner.PlanInput, turnID string, it confirmationAwait, dec interrupt.ConfirmationDecision) error {
	return r.publishHook(ctx, hooks.NewToolAuthorizationEvent(
		base.RunContext.RunID,
		input.AgentID,
		base.RunContext.SessionID,
		it.call.Name,
		it.call.ToolCallID,
		dec.Approved,
		it.plan.Prompt,
		dec.RequestedBy,
	), turnID)
}

func (r *Runtime) handleDeniedConfirmation(ctx context.Context, base *planner.PlanInput, st *runLoopState, turnID string, expectedChildren int, it confirmationAwait) ([]*planner.ToolResult, *RunOutput, error) {
	deniedResult := it.plan.DeniedResult
	if err := r.publishDeniedConfirmationEvents(ctx, turnID, expectedChildren, it, deniedResult); err != nil {
		return nil, nil, err
	}
	tr := &planner.ToolResult{
		Name:       it.call.Name,
		ToolCallID: it.call.ToolCallID,
		Result:     deniedResult,
	}
	if err := r.recordConfirmationToolResult(ctx, base, st, it.call, tr); err != nil {
		return nil, nil, err
	}
	return []*planner.ToolResult{tr}, nil, nil
}

func (r *Runtime) publishDeniedConfirmationEvents(ctx context.Context, turnID string, expectedChildren int, it confirmationAwait, deniedResult any) error {
	if err := r.publishHook(ctx, hooks.NewToolCallScheduledEvent(
		it.call.RunID,
		it.call.AgentID,
		it.call.SessionID,
		it.call.Name,
		it.call.ToolCallID,
		it.call.Payload,
		"",
		it.call.ParentToolCallID,
		expectedChildren,
	), turnID); err != nil {
		return err
	}
	resultJSON, err := r.marshalToolValue(ctx, it.call.Name, deniedResult, nil)
	if err != nil {
		return fmt.Errorf("encode %s denied tool result for streaming: %w", it.call.Name, err)
	}
	return r.publishHook(ctx, hooks.NewToolResultReceivedEvent(
		it.call.RunID,
		it.call.AgentID,
		it.call.SessionID,
		it.call.Name,
		it.call.ToolCallID,
		it.call.ParentToolCallID,
		deniedResult,
		rawjson.Message(resultJSON),
		nil,
		formatResultPreview(it.call.Name, deniedResult, nil),
		nil,
		0,
		nil,
		nil,
		nil,
	), turnID)
}

func (r *Runtime) recordConfirmationToolResult(ctx context.Context, base *planner.PlanInput, st *runLoopState, call planner.ToolRequest, tr *planner.ToolResult) error {
	st.ToolEvents = append(st.ToolEvents, cloneToolResults([]*planner.ToolResult{tr})...)
	if err := r.appendToolOutputs(ctx, st, []planner.ToolRequest{call}, []*planner.ToolResult{tr}); err != nil {
		return err
	}
	return r.appendUserToolResults(base, []planner.ToolRequest{call}, []*planner.ToolResult{tr}, st.Ledger)
}

func (r *Runtime) executeConfirmedToolCall(ctx context.Context, wfCtx engine.WorkflowContext, reg AgentRegistration, input *RunInput, base *planner.PlanInput, st *runLoopState, toolOpts engine.ActivityOptions, expectedChildren int, parentTracker *childTracker, turnID string, deadlines *runDeadlines, it confirmationAwait) ([]*planner.ToolResult, *RunOutput, error) {
	call := it.call
	if call.ToolCallID == "" {
		call.ToolCallID = generateDeterministicToolCallID(base.RunContext.RunID, call.TurnID, base.RunContext.Attempt, call.Name, 0)
	}
	grouped, timeouts := r.groupToolCallsByTimeout([]planner.ToolRequest{call}, input, toolOpts.StartToCloseTimeout)
	finishBy := confirmationFinishBy(deadlines)
	vals, timedOut, err := r.executeGroupedToolCalls(
		wfCtx,
		reg,
		input.AgentID,
		base,
		expectedChildren,
		parentTracker,
		finishBy,
		grouped,
		timeouts,
		toolOpts,
	)
	if err != nil {
		return nil, nil, err
	}
	if err := r.recordExecutedConfirmationResults(ctx, base, st, call, vals); err != nil {
		return nil, nil, err
	}
	if timedOut {
		out, err := r.finalizeWithPlanner(wfCtx, reg, input, base, st.ToolEvents, st.ToolOutputs, st.AggUsage, st.NextAttempt, turnID, planner.TerminationReasonTimeBudget, deadlines.Hard)
		return nil, out, err
	}
	return vals, nil, nil
}

func confirmationFinishBy(deadlines *runDeadlines) time.Time {
	if deadlines == nil || deadlines.Hard.IsZero() {
		return time.Time{}
	}
	return deadlines.Hard.Add(-deadlines.finalizeReserve())
}

func (r *Runtime) recordExecutedConfirmationResults(ctx context.Context, base *planner.PlanInput, st *runLoopState, call planner.ToolRequest, vals []*planner.ToolResult) error {
	st.ToolEvents = append(st.ToolEvents, cloneToolResults(vals)...)
	if err := r.appendToolOutputs(ctx, st, []planner.ToolRequest{call}, vals); err != nil {
		return err
	}
	return r.appendUserToolResults(base, []planner.ToolRequest{call}, vals, st.Ledger)
}
