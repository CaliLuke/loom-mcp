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

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/api"
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
	output, aggUsage, err := r.runFinalizationPlan(wfCtx, reg, input, base, allToolOutputs, aggUsage, nextAttempt, turnID, reason, hardDeadline)
	if err != nil {
		return nil, err
	}
	toolEvents, err := r.encodeToolEvents(ctx, allToolResults)
	if err != nil {
		return nil, err
	}
	return buildFinalizedRunOutput(input.AgentID, base, output, toolEvents, aggUsage), nil
}

func (r *Runtime) runFinalizationPlan(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
	allToolOutputs []*planner.ToolOutput,
	aggUsage model.TokenUsage,
	nextAttempt int,
	turnID string,
	reason planner.TerminationReason,
	hardDeadline time.Time,
) (*PlanActivityOutput, model.TokenUsage, error) {
	ctx := wfCtx.Context()
	req, reasonText, resumeOpts, err := r.prepareFinalizePlan(ctx, reg, input, base, allToolOutputs, nextAttempt, turnID, reason)
	if err != nil {
		return nil, aggUsage, err
	}
	output, err := r.runPlanActivity(wfCtx, reg.ResumeActivityName, resumeOpts, req, hardDeadline)
	if err != nil {
		return nil, aggUsage, fmt.Errorf("%s: %w", reasonText, err)
	}
	if err := validateFinalizePlanOutput(output, reasonText); err != nil {
		return nil, aggUsage, err
	}
	aggUsage = addTokenUsage(aggUsage, output.Usage)
	if err := r.publishFinalizeOutput(ctx, base, input, turnID, output); err != nil {
		return nil, aggUsage, err
	}
	return output, aggUsage, nil
}

func validateFinalizePlanOutput(output *PlanActivityOutput, reasonText string) error {
	if output == nil || output.Result == nil {
		return fmt.Errorf("%s", reasonText)
	}
	if err := validateTerminalPlanResult(output.Result); err != nil {
		return fmt.Errorf("%s: %w", reasonText, err)
	}
	return nil
}

func (r *Runtime) publishFinalizeOutput(
	ctx context.Context,
	base *planner.PlanInput,
	input *RunInput,
	turnID string,
	output *PlanActivityOutput,
) error {
	finalMsg := finalPlannerMessage(output)
	if output.Result.FinalResponse != nil && !output.Result.Streamed {
		if err := r.publishFinalizationAssistantMessage(ctx, base, input, turnID, finalMsg); err != nil {
			return err
		}
	}
	return r.publishPlannerNotes(ctx, base, input, turnID, output.Result.Notes)
}

func buildFinalizedRunOutput(
	agentID agent.Ident,
	base *planner.PlanInput,
	output *PlanActivityOutput,
	toolEvents []*api.ToolEvent,
	aggUsage model.TokenUsage,
) *RunOutput {
	return &RunOutput{
		AgentID:         agentID,
		RunID:           base.RunContext.RunID,
		Final:           finalPlannerMessage(output),
		FinalToolResult: finalToolResultEvent(base.RunContext.Tool, output.Result.FinalToolResult),
		ToolEvents:      toolEvents,
		Notes:           clonePlannerNotes(output.Result.Notes),
		Usage:           &aggUsage,
	}
}

func (r *Runtime) prepareFinalizePlan(
	ctx context.Context,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
	allToolOutputs []*planner.ToolOutput,
	nextAttempt int,
	turnID string,
	reason planner.TerminationReason,
) (PlanActivityInput, string, engine.ActivityOptions, error) {
	if err := r.publishFinalizingPhase(ctx, base, input, turnID); err != nil {
		return PlanActivityInput{}, "", engine.ActivityOptions{}, err
	}
	req, reasonText, err := r.buildFinalizePlanRequest(base, input, allToolOutputs, nextAttempt, reason)
	if err != nil {
		return PlanActivityInput{}, "", engine.ActivityOptions{}, err
	}
	if err := r.publishFinalizeTransition(ctx, base, input, turnID, reason); err != nil {
		return PlanActivityInput{}, "", engine.ActivityOptions{}, err
	}
	return req, reasonText, resolveResumeActivityOptions(reg, input), nil
}

func (r *Runtime) buildFinalizePlanRequest(
	base *planner.PlanInput,
	input *RunInput,
	allToolOutputs []*planner.ToolOutput,
	nextAttempt int,
	reason planner.TerminationReason,
) (PlanActivityInput, string, error) {
	hint := finalizationHint(reason)
	resumeCtx := base.RunContext
	resumeCtx.Attempt = nextAttempt
	resumeCtx.MaxDuration = "0s"
	encodedToolOutputs, err := encodePlannerToolOutputs(allToolOutputs)
	if err != nil {
		return PlanActivityInput{}, "", err
	}
	req := PlanActivityInput{
		AgentID:     input.AgentID,
		RunID:       base.RunContext.RunID,
		Messages:    prepareFinalizationMessages(base.Messages, hint),
		RunContext:  resumeCtx,
		ToolOutputs: encodedToolOutputs,
		Finalize:    &planner.Termination{Reason: reason, Message: hint},
	}
	if err := enforcePlanActivityInputBudget(req); err != nil {
		return PlanActivityInput{}, "", err
	}
	return req, finalizationReasonText(reason), nil
}

func finalPlannerMessage(output *PlanActivityOutput) *model.Message {
	if output.Result.FinalResponse == nil {
		return nil
	}
	finalMsg := output.Result.FinalResponse.Message
	if output.Result.Streamed && agentMessageText(finalMsg) == "" {
		if text := transcriptText(output.Transcript); text != "" {
			return newTextAgentMessage(model.ConversationRoleAssistant, text)
		}
	}
	return finalMsg
}

func clonePlannerNotes(in []planner.PlannerAnnotation) []*planner.PlannerAnnotation {
	out := make([]*planner.PlannerAnnotation, len(in))
	for i := range in {
		out[i] = &in[i]
	}
	return out
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
		resumeReq, err := r.awaitInterruptResume(ctx, wfCtx, input, turnID, ctrl, budgetDeadline, req)
		if err != nil || resumeReq == nil {
			return err
		}
		applyResumeMessages(base, nextAttempt, resumeReq)
		if err := r.publishResumed(ctx, input, turnID, resumeReq); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) awaitInterruptResume(
	ctx context.Context,
	wfCtx engine.WorkflowContext,
	input *RunInput,
	turnID string,
	ctrl *interrupt.Controller,
	budgetDeadline time.Time,
	req interrupt.PauseRequest,
) (interrupt.ResumeRequest, error) {
	if req == nil {
		return nil, errors.New("pause: received nil pause request")
	}
	if err := r.publishPauseEvent(ctx, input, turnID, req); err != nil {
		return nil, err
	}
	timeout, ok := timeoutUntil(budgetDeadline, wfCtx.Now())
	if !ok {
		return nil, r.publishResumeReason(ctx, input, turnID, "deadline_exceeded")
	}
	resumeReq, err := ctrl.WaitResume(ctx, timeout)
	if err != nil {
		return nil, r.handleResumeWaitError(ctx, input, turnID, err)
	}
	if resumeReq == nil {
		return nil, errors.New("resume: received nil resume request")
	}
	return resumeReq, nil
}

func (r *Runtime) handleResumeWaitError(ctx context.Context, input *RunInput, turnID string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return r.publishResumeReason(ctx, input, turnID, "deadline_exceeded")
	}
	if err2 := r.publishResumeReason(ctx, input, turnID, "resume_error"); err2 != nil {
		return err2
	}
	return err
}

func applyResumeMessages(base *planner.PlanInput, nextAttempt *int, resumeReq interrupt.ResumeRequest) {
	if len(resumeReq.Messages) > 0 {
		base.Messages = append(base.Messages, resumeReq.Messages...)
	}
	base.RunContext.Attempt = *nextAttempt
	*nextAttempt++
}

func resolveResumeActivityOptions(reg AgentRegistration, input *RunInput) engine.ActivityOptions {
	opts := reg.ResumeActivityOptions
	if input.Policy != nil && input.Policy.PlanTimeout > 0 {
		opts.StartToCloseTimeout = input.Policy.PlanTimeout
	}
	return opts
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
	if err := r.publishMissingFieldAwaitClarification(ctx, input, base, turnID, awaitID, mf); err != nil {
		return nil, err
	}
	ans, err := waitForClarificationAnswer(wfCtx, ctx, ctrl, deadlines)
	if err != nil {
		if err2 := r.publishClarificationError(ctx, input, base, turnID, awaitID); err2 != nil {
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
	appendClarificationAnswer(base, ans.Answer)
	if err := r.publishClarificationResumed(ctx, input, base, turnID, ans); err != nil {
		return nil, err
	}
	return nil, nil
}

func (r *Runtime) publishMissingFieldAwaitClarification(
	ctx context.Context,
	input *RunInput,
	base *planner.PlanInput,
	turnID string,
	awaitID string,
	mf *planner.RetryHint,
) error {
	var restrict tools.Ident
	if mf.RestrictToTool {
		restrict = mf.Tool
	}
	if err := r.publishHook(ctx, hooks.NewAwaitClarificationEvent(
		base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, awaitID, mf.ClarifyingQuestion, mf.MissingFields, restrict, mf.ExampleInput,
	), turnID); err != nil {
		return err
	}
	return r.publishHook(ctx, hooks.NewRunPausedEvent(
		base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, "await_clarification", "runtime", nil, nil,
	), turnID)
}

func waitForClarificationAnswer(
	wfCtx engine.WorkflowContext,
	ctx context.Context,
	ctrl *interrupt.Controller,
	deadlines *runDeadlines,
) (interrupt.ClarificationAnswer, error) {
	waitStartedAt := wfCtx.Now()
	ans, err := ctrl.WaitProvideClarification(ctx, 0)
	if deadlines != nil {
		if delta := wfCtx.Now().Sub(waitStartedAt); delta > 0 {
			deadlines.pause(delta)
		}
	}
	return ans, err
}

func (r *Runtime) publishClarificationError(
	ctx context.Context,
	input *RunInput,
	base *planner.PlanInput,
	turnID string,
	awaitID string,
) error {
	return r.publishHook(ctx, hooks.NewRunResumedEvent(
		base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, "clarification_error", "runtime",
		map[string]string{"resumed_by": "clarification_error", "await_id": awaitID}, 0,
	), turnID)
}

func appendClarificationAnswer(base *planner.PlanInput, answer string) {
	if answer == "" {
		return
	}
	base.Messages = append(base.Messages, &model.Message{
		Role:  model.ConversationRoleUser,
		Parts: []model.Part{model.TextPart{Text: answer}},
	})
}

func (r *Runtime) publishClarificationResumed(
	ctx context.Context,
	input *RunInput,
	base *planner.PlanInput,
	turnID string,
	ans interrupt.ClarificationAnswer,
) error {
	return r.publishHook(ctx, hooks.NewRunResumedEvent(
		base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, "clarification_provided", input.RunID, ans.Labels, 1,
	), turnID)
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
	callOpts := capPlanActivityOptions(wfCtx, options, hardDeadline)
	out, err := wfCtx.ExecutePlannerActivity(wfCtx.Context(), engine.PlannerActivityCall{
		Name:    activityName,
		Input:   &input,
		Options: callOpts,
	})
	if err != nil {
		return nil, err
	}
	if err := validatePlanActivityOutput(out); err != nil {
		return nil, err
	}
	r.logPlanActivityResult(wfCtx.Context(), out)
	return out, nil
}

func capPlanActivityOptions(wfCtx engine.WorkflowContext, options engine.ActivityOptions, hardDeadline time.Time) engine.ActivityOptions {
	callOpts := options
	startToClose := options.StartToCloseTimeout
	scheduleToStart := options.ScheduleToStartTimeout
	if !hardDeadline.IsZero() {
		if rem := hardDeadline.Sub(wfCtx.Now()); rem > 0 {
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
	return callOpts
}

func validatePlanActivityOutput(out *PlanActivityOutput) error {
	if out == nil {
		return fmt.Errorf("runPlanActivity received nil PlanActivityOutput")
	}
	if out.Result == nil {
		return fmt.Errorf("runPlanActivity received nil PlanResult")
	}
	if len(out.Result.ToolCalls) == 0 &&
		out.Result.FinalResponse == nil &&
		out.Result.FinalToolResult == nil &&
		out.Result.Await == nil {
		return fmt.Errorf("runPlanActivity received PlanResult with no ToolCalls, FinalResponse, FinalToolResult, or Await")
	}
	return nil
}

func (r *Runtime) logPlanActivityResult(ctx context.Context, out *PlanActivityOutput) {
	r.logger.Info(ctx,
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
}
