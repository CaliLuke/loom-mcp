package runtime

// workflow_support.go contains the workflow-only helper methods used by the plan/tool loop.
//
// Contract:
// - These helpers are deterministic and replay-safe: timeouts use workflow time.
// - Callers should only invoke them from within workflow execution (e.g. ExecuteWorkflow/runLoop).
// - The helpers publish lifecycle events via hooks so streams can close deterministically.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

// finalizeWithPlanner asks the planner for a tool-free final response and returns it as RunOutput.
func (r *Runtime) finalizeWithPlanner(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
	allToolResults []*planner.ToolResult,
	allToolOutputs []*planner.ToolOutput,
	aggUsage model.TokenUsage,
	nextAttempt int,
	turnID string,
	reason planner.TerminationReason,
	hardDeadline time.Time,
) (*RunOutput, error) {
	if base == nil {
		return nil, errors.New("base plan input is required")
	}
	ctx := wfCtx.Context()
	if err := r.publishFinalizingPhase(ctx, base, input, turnID); err != nil {
		return nil, err
	}
	hint := finalizationHint(reason)
	messages := prepareFinalizationMessages(base.Messages, hint)
	resumeCtx := base.RunContext
	resumeCtx.Attempt = nextAttempt
	resumeCtx.MaxDuration = "0s"
	encodedToolOutputs, err := encodePlannerToolOutputs(allToolOutputs)
	if err != nil {
		return nil, err
	}
	req := PlanActivityInput{
		AgentID:     input.AgentID,
		RunID:       base.RunContext.RunID,
		Messages:    messages,
		RunContext:  resumeCtx,
		ToolOutputs: encodedToolOutputs,
		Finalize:    &planner.Termination{Reason: reason, Message: hint},
	}
	if err := enforcePlanActivityInputBudget(req); err != nil {
		return nil, err
	}
	if err := r.publishFinalizeTransition(ctx, base, input, turnID, reason); err != nil {
		return nil, err
	}
	reasonText := finalizationReasonText(reason)
	resumeOpts := reg.ResumeActivityOptions
	if input.Policy != nil && input.Policy.PlanTimeout > 0 {
		resumeOpts.StartToCloseTimeout = input.Policy.PlanTimeout
	}
	output, err := r.runPlanActivity(wfCtx, reg.ResumeActivityName, resumeOpts, req, hardDeadline)
	if err != nil {
		// Surface the termination reason prominently; include underlying error for observability.
		return nil, fmt.Errorf("%s: %w", reasonText, err)
	}
	if output == nil || output.Result == nil {
		return nil, fmt.Errorf("%s", reasonText)
	}
	if err := validateTerminalPlanResult(output.Result); err != nil {
		return nil, fmt.Errorf("%s: %w", reasonText, err)
	}
	aggUsage = addTokenUsage(aggUsage, output.Usage)
	var finalMsg *model.Message
	if output.Result.FinalResponse != nil {
		finalMsg = output.Result.FinalResponse.Message
		if output.Result.Streamed && agentMessageText(finalMsg) == "" {
			if text := transcriptText(output.Transcript); text != "" {
				finalMsg = newTextAgentMessage(model.ConversationRoleAssistant, text)
			}
		}
	}
	if output.Result.FinalResponse != nil && !output.Result.Streamed {
		if err := r.publishFinalizationAssistantMessage(ctx, base, input, turnID, finalMsg); err != nil {
			return nil, err
		}
	}
	if err := r.publishPlannerNotes(ctx, base, input, turnID, output.Result.Notes); err != nil {
		return nil, err
	}
	notes := make([]*planner.PlannerAnnotation, len(output.Result.Notes))
	for i := range output.Result.Notes {
		notes[i] = &output.Result.Notes[i]
	}
	toolEvents, err := r.encodeToolEvents(ctx, allToolResults)
	if err != nil {
		return nil, err
	}
	finalToolResult := finalToolResultEvent(base.RunContext.Tool, output.Result.FinalToolResult)

	return &RunOutput{
		AgentID:         input.AgentID,
		RunID:           base.RunContext.RunID,
		Final:           finalMsg,
		FinalToolResult: finalToolResult,
		ToolEvents:      toolEvents,
		Notes:           notes,
		Usage:           &aggUsage,
	}, nil
}

// handleInterrupts drains pause signals and blocks until a resume signal arrives.
// When budgetDeadline is reached, it returns nil so the caller can finalize cleanly.
func (r *Runtime) handleInterrupts(
	wfCtx engine.WorkflowContext,
	input *RunInput,
	base *planner.PlanInput,
	turnID string,
	ctrl *interrupt.Controller,
	nextAttempt *int,
	budgetDeadline time.Time,
) error {
	if ctrl == nil {
		return nil
	}
	ctx := wfCtx.Context()
	for {
		req, ok := ctrl.PollPause()
		if !ok {
			break
		}
		if req == nil {
			return errors.New("pause: received nil pause request")
		}
		if err := r.publishPauseEvent(ctx, input, turnID, req); err != nil {
			return err
		}
		timeout, ok := timeoutUntil(budgetDeadline, wfCtx.Now())
		if !ok {
			if err := r.publishResumeReason(ctx, input, turnID, "deadline_exceeded"); err != nil {
				return err
			}
			return nil
		}
		resumeReq, err := ctrl.WaitResume(ctx, timeout)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				if err := r.publishResumeReason(ctx, input, turnID, "deadline_exceeded"); err != nil {
					return err
				}
				return nil
			}
			if err2 := r.publishResumeReason(ctx, input, turnID, "resume_error"); err2 != nil {
				return err2
			}
			return err
		}
		if resumeReq == nil {
			return errors.New("resume: received nil resume request")
		}
		if len(resumeReq.Messages) > 0 {
			base.Messages = append(base.Messages, resumeReq.Messages...)
		}
		base.RunContext.Attempt = *nextAttempt
		*nextAttempt++
		if err := r.publishResumed(ctx, input, turnID, resumeReq); err != nil {
			return err
		}
	}
	return nil
}

// handleMissingFieldsPolicy inspects tool results for a RetryHint indicating missing
// required fields and applies the agent RunPolicy.OnMissingFields behavior:
//
//   - MissingFieldsFinalize: immediately finalize by requesting a tool-free final answer
//     from the planner. Returns a non-nil RunOutput to short-circuit the loop.
//   - MissingFieldsAwaitClarification: when durable (interrupt controller present), emit
//     an await_clarification event, pause the run, and wait indefinitely for operator input.
//     On resume, append the user answer to base PlanInput so the next turn can proceed.
//   - MissingFieldsResume (or unspecified): do nothing; the planner will see RetryHints
//     and may choose how to proceed. Returns handled=false.
//
// The function returns:
//   - out: non-nil only when finalization occurred
//   - err: any error encountered while pausing/resuming
func (r *Runtime) handleMissingFieldsPolicy(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
	results []*planner.ToolResult,
	allResults []*planner.ToolResult,
	allToolOutputs []*planner.ToolOutput,
	aggUsage model.TokenUsage,
	nextAttempt *int,
	turnID string,
	ctrl *interrupt.Controller,
	deadlines *runDeadlines,
) (*RunOutput, error) {
	if ctrl == nil || reg.Policy.OnMissingFields == "" {
		return nil, nil
	}
	mf, triggerTool, triggerCall := firstMissingFieldsHint(results)
	if mf == nil {
		return nil, nil
	}
	switch reg.Policy.OnMissingFields {
	case MissingFieldsFinalize:
		out, err := r.finalizeWithPlanner(wfCtx, reg, input, base, allResults, allToolOutputs, aggUsage, *nextAttempt, turnID, planner.TerminationReasonFailureCap, time.Time{})
		return out, err
	case MissingFieldsAwaitClarification:
		return r.awaitMissingFieldClarification(wfCtx, input, base, turnID, ctrl, deadlines, mf, triggerTool, triggerCall)
	case MissingFieldsResume:
		return nil, nil
	default:
		return nil, nil
	}
}

func (r *Runtime) publishFinalizingPhase(ctx context.Context, base *planner.PlanInput, input *RunInput, turnID string) error {
	return r.publishHook(ctx, hooks.NewRunPhaseChangedEvent(base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, run.PhaseSynthesizing), turnID)
}

func finalizationHint(reason planner.TerminationReason) string {
	switch reason {
	case planner.TerminationReasonTimeBudget:
		return "FINALIZE NOW: time budget reached.\n\n- Provide the best possible final answer using ONLY the information already available in the conversation and tool results.\n- Do NOT call any tools.\n- Do NOT say you will call tools or that you will \"try\" another approach.\n- If additional tool calls would be needed, explain what you would have retrieved and how it would change the answer, then provide the best provisional answer."
	case planner.TerminationReasonToolCap:
		return "FINALIZE NOW: tool budget exhausted.\n\n- Provide the best possible final answer using ONLY the information already available in the conversation and tool results.\n- Do NOT call any tools.\n- Do NOT say you will call tools.\n- If further tool calls would be needed, describe them briefly and provide the best provisional answer."
	case planner.TerminationReasonFailureCap:
		return "FINALIZE NOW: too many tool failures.\n\n- Provide the best possible final answer using ONLY the information already available in the conversation and tool results.\n- Do NOT call any tools.\n- Do NOT say you will call tools.\n- If tools failed due to invalid arguments, summarize the failure and provide a corrected plan/payload shape (without actually calling tools), then provide the best provisional answer."
	default:
		return "FINALIZE NOW.\n\n- Provide the best possible final answer using ONLY the information already available in the conversation and tool results.\n- Do NOT call any tools.\n- Do NOT say you will call tools.\n- If more work is needed, describe it succinctly and provide the best provisional answer."
	}
}

func prepareFinalizationMessages(messages []*model.Message, hint string) []*model.Message {
	out := cloneMessages(messages)
	out = appendSyntheticToolResultsForFinalize(out)
	if hint != "" {
		out = append(out, &model.Message{
			Role:  model.ConversationRoleSystem,
			Parts: []model.Part{model.TextPart{Text: hint}},
		})
	}
	return out
}

func appendSyntheticToolResultsForFinalize(messages []*model.Message) []*model.Message {
	if len(messages) == 0 {
		return messages
	}
	last := messages[len(messages)-1]
	if last.Role != model.ConversationRoleAssistant {
		return messages
	}
	var parts []model.Part
	for _, p := range last.Parts {
		if tu, ok := p.(model.ToolUsePart); ok {
			parts = append(parts, model.ToolResultPart{
				ToolUseID: tu.ID,
				Content:   "Finalized before a tool result was provided for this request.",
				IsError:   true,
			})
		}
	}
	if len(parts) == 0 {
		return messages
	}
	return append(messages, &model.Message{Role: model.ConversationRoleUser, Parts: parts})
}

func (r *Runtime) publishFinalizeTransition(ctx context.Context, base *planner.PlanInput, input *RunInput, turnID string, reason planner.TerminationReason) error {
	if err := r.publishHook(ctx, hooks.NewRunPausedEvent(
		base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, "finalize", "runtime", map[string]string{"reason": string(reason)}, nil,
	), turnID); err != nil {
		return err
	}
	return r.publishHook(ctx, hooks.NewRunResumedEvent(
		base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, "finalize", base.RunContext.RunID, nil, 0,
	), turnID)
}

func finalizationReasonText(reason planner.TerminationReason) string {
	switch reason {
	case planner.TerminationReasonTimeBudget:
		return "time budget exceeded"
	case planner.TerminationReasonToolCap:
		return "tool call cap exceeded"
	case planner.TerminationReasonFailureCap:
		return "consecutive failed tool call cap exceeded"
	default:
		return "finalization failed"
	}
}

func (r *Runtime) publishFinalizationAssistantMessage(ctx context.Context, base *planner.PlanInput, input *RunInput, turnID string, msg *model.Message) error {
	return r.publishHook(ctx, hooks.NewAssistantMessageEvent(
		base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, agentMessageText(msg), nil,
	), turnID)
}

func (r *Runtime) publishPlannerNotes(ctx context.Context, base *planner.PlanInput, input *RunInput, turnID string, notes []planner.PlannerAnnotation) error {
	for _, note := range notes {
		if err := r.publishHook(ctx, hooks.NewPlannerNoteEvent(
			base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, note.Text, note.Labels,
		), turnID); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) publishPauseEvent(ctx context.Context, input *RunInput, turnID string, req interrupt.PauseRequest) error {
	return r.publishHook(ctx, hooks.NewRunPausedEvent(
		input.RunID, input.AgentID, input.SessionID, req.Reason, req.RequestedBy, req.Labels, req.Metadata,
	), turnID)
}

func (r *Runtime) publishResumeReason(ctx context.Context, input *RunInput, turnID string, reason string) error {
	return r.publishHook(ctx, hooks.NewRunResumedEvent(
		input.RunID, input.AgentID, input.SessionID, reason, "runtime", map[string]string{"resumed_by": reason}, 0,
	), turnID)
}

func (r *Runtime) publishResumed(ctx context.Context, input *RunInput, turnID string, req interrupt.ResumeRequest) error {
	return r.publishHook(ctx, hooks.NewRunResumedEvent(
		input.RunID, input.AgentID, input.SessionID, req.Notes, req.RequestedBy, req.Labels, len(req.Messages),
	), turnID)
}

func firstMissingFieldsHint(results []*planner.ToolResult) (*planner.RetryHint, tools.Ident, string) {
	for _, tr := range results {
		if tr == nil || tr.RetryHint == nil {
			continue
		}
		if tr.RetryHint.Reason == planner.RetryReasonMissingFields {
			return tr.RetryHint, tr.Name, tr.ToolCallID
		}
	}
	return nil, "", ""
}

func (r *Runtime) awaitMissingFieldClarification(
	wfCtx engine.WorkflowContext,
	input *RunInput,
	base *planner.PlanInput,
	turnID string,
	ctrl *interrupt.Controller,
	deadlines *runDeadlines,
	mf *planner.RetryHint,
	triggerTool tools.Ident,
	triggerCall string,
) (*RunOutput, error) {
	ctx := wfCtx.Context()
	awaitID := generateDeterministicAwaitID(base.RunContext.RunID, base.RunContext.TurnID, triggerTool, triggerCall)
	var restrict tools.Ident
	if mf.RestrictToTool {
		restrict = mf.Tool
	}
	if err := r.publishHook(ctx, hooks.NewAwaitClarificationEvent(
		base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, awaitID, mf.ClarifyingQuestion, mf.MissingFields, restrict, mf.ExampleInput,
	), turnID); err != nil {
		return nil, err
	}
	if err := r.publishHook(ctx, hooks.NewRunPausedEvent(
		base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, "await_clarification", "runtime", nil, nil,
	), turnID); err != nil {
		return nil, err
	}
	waitStartedAt := wfCtx.Now()
	ans, err := ctrl.WaitProvideClarification(ctx, 0)
	if deadlines != nil {
		if delta := wfCtx.Now().Sub(waitStartedAt); delta > 0 {
			deadlines.pause(delta)
		}
	}
	if err != nil {
		if err2 := r.publishHook(ctx, hooks.NewRunResumedEvent(
			base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, "clarification_error", "runtime",
			map[string]string{"resumed_by": "clarification_error", "await_id": awaitID}, 0,
		), turnID); err2 != nil {
			return nil, err2
		}
		return nil, err
	}
	if ans == nil {
		return nil, errors.New("await_clarification: received nil clarification answer")
	}
	if ans.ID != "" && ans.ID != awaitID {
		return nil, fmt.Errorf("unexpected await ID for clarification")
	}
	if ans.Answer != "" {
		base.Messages = append(base.Messages, &model.Message{
			Role: model.ConversationRoleUser,
			Parts: []model.Part{model.TextPart{
				Text: ans.Answer,
			}},
		})
	}
	if err := r.publishHook(ctx, hooks.NewRunResumedEvent(
		base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, "clarification_provided", input.RunID, ans.Labels, 1,
	), turnID); err != nil {
		return nil, err
	}
	return nil, nil
}

// runPlanActivity schedules a plan/resume activity with the configured options.
func (r *Runtime) runPlanActivity(
	wfCtx engine.WorkflowContext,
	activityName string,
	options engine.ActivityOptions,
	input PlanActivityInput,
	hardDeadline time.Time,
) (*PlanActivityOutput, error) {
	if activityName == "" {
		return nil, errors.New("plan activity not registered")
	}
	callOpts := options
	// Cap queue wait and attempt time to the remaining hard deadline so finalizer
	// handling stays deterministic even when workers are unavailable.
	startToClose := options.StartToCloseTimeout
	scheduleToStart := options.ScheduleToStartTimeout
	if !hardDeadline.IsZero() {
		now := wfCtx.Now()
		if rem := hardDeadline.Sub(now); rem > 0 {
			if startToClose == 0 || startToClose > rem {
				startToClose = rem
			}
			if scheduleToStart == 0 || scheduleToStart > rem {
				scheduleToStart = rem
			}
		}
	}
	callOpts.StartToCloseTimeout = startToClose
	callOpts.ScheduleToStartTimeout = scheduleToStart

	out, err := wfCtx.ExecutePlannerActivity(wfCtx.Context(), engine.PlannerActivityCall{
		Name:    activityName,
		Input:   &input,
		Options: callOpts,
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("runPlanActivity received nil PlanActivityOutput")
	}
	if out.Result == nil {
		return nil, fmt.Errorf("runPlanActivity received nil PlanResult")
	}
	if len(out.Result.ToolCalls) == 0 &&
		out.Result.FinalResponse == nil &&
		out.Result.FinalToolResult == nil &&
		out.Result.Await == nil {
		return nil, fmt.Errorf("runPlanActivity received PlanResult with no ToolCalls, FinalResponse, FinalToolResult, or Await")
	}
	r.logger.Info(wfCtx.Context(),
		"runPlanActivity received PlanResult",
		"tool_calls",
		len(out.Result.ToolCalls),
		"final_response",
		out.Result.FinalResponse != nil,
		"final_tool_result",
		out.Result.FinalToolResult != nil,
		"await",
		out.Result.Await != nil,
	)
	return out, nil
}
