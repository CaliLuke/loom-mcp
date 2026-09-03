package runtime

// workflow_finalize.go contains the finalize-phase helpers used by the plan/tool loop.
//
// Contract:
// - These helpers are deterministic and replay-safe: timeouts use workflow time.
// - Callers should only invoke them from within workflow execution.
// - The helpers publish lifecycle events via hooks so streams can close deterministically.

import (
	"context"
	"errors"
	"fmt"
	"time"

	agent "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
)

// finalizeWithPlanner executes the configured limit plan or asks the finalizer
// for a final response or terminal bookkeeping call.
func (r *Runtime) finalizeWithPlanner(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
	allToolResults []*planner.ToolResult,
	allToolOutputs []*planner.ToolOutput,
	aggUsage model.TokenUsage,
	caps policy.CapsState,
	nextAttempt int,
	turnID string,
	notes []planner.PlannerAnnotation,
	reason planner.TerminationReason,
	hardDeadline time.Time,
) (*RunOutput, error) {
	if base == nil {
		return nil, errors.New("base plan input is required")
	}
	if completion := completionTool(input); completion != "" {
		return nil, completionToolRequiredError(completion, reason)
	}
	out, handled, err := r.executeFixedLimitTerminalPlan(
		wfCtx, reg, input, base, allToolResults, aggUsage, caps, nextAttempt, turnID, notes, reason, hardDeadline,
	)
	if err != nil || handled {
		return out, err
	}
	return r.finalizeWithModelPlan(
		wfCtx, reg, input, base, allToolResults, allToolOutputs, aggUsage, caps, nextAttempt, turnID, reason, hardDeadline,
	)
}

func (r *Runtime) executeFixedLimitTerminalPlan(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
	allToolResults []*planner.ToolResult,
	aggUsage model.TokenUsage,
	caps policy.CapsState,
	nextAttempt int,
	turnID string,
	notes []planner.PlannerAnnotation,
	reason planner.TerminationReason,
	hardDeadline time.Time,
) (*RunOutput, bool, error) {
	var plans *LimitTerminalPlans
	if input.Policy != nil {
		plans = input.Policy.LimitTerminalPlans
	}
	call, ok, err := limitTerminalCall(plans, reason)
	if err != nil || !ok {
		return nil, false, err
	}
	ctx := wfCtx.Context()
	if err := r.publishFinalizingPhase(ctx, base, input, turnID); err != nil {
		return nil, true, err
	}
	if err := r.publishFinalizeTransition(ctx, base, input, turnID, reason); err != nil {
		return nil, true, err
	}
	out, err := r.executeFinalizationToolCalls(
		wfCtx,
		reg,
		input,
		base,
		[]planner.ToolRequest{{Name: call.Name, Payload: call.Payload}},
		allToolResults,
		aggUsage,
		caps,
		nextAttempt,
		turnID,
		reason,
		hardDeadline,
	)
	if err != nil {
		return nil, true, err
	}
	if err := r.publishPlannerNotes(ctx, base, input, turnID, notes); err != nil {
		return nil, true, err
	}
	out.Notes = clonePlannerNotes(notes)
	return out, true, nil
}

func (r *Runtime) finalizeWithModelPlan(
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
	allToolResults []*planner.ToolResult,
	allToolOutputs []*planner.ToolOutput,
	aggUsage model.TokenUsage,
	caps policy.CapsState,
	nextAttempt int,
	turnID string,
	reason planner.TerminationReason,
	hardDeadline time.Time,
) (*RunOutput, error) {
	output, aggUsage, err := r.runFinalizationPlan(
		wfCtx, reg, input, base, allToolOutputs, aggUsage, caps, nextAttempt, turnID, reason, hardDeadline,
	)
	if err != nil {
		return nil, err
	}
	if len(output.Result.ToolCalls) > 0 {
		out, executeErr := r.executeFinalizationToolCalls(
			wfCtx,
			reg,
			input,
			base,
			output.Result.ToolCalls,
			allToolResults,
			aggUsage,
			caps,
			nextAttempt,
			turnID,
			reason,
			hardDeadline,
		)
		if out != nil {
			out.Notes = clonePlannerNotes(output.Result.Notes)
		}
		return out, executeErr
	}
	toolEvents, err := r.encodeToolEvents(wfCtx.Context(), allToolResults)
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
	caps policy.CapsState,
	nextAttempt int,
	turnID string,
	reason planner.TerminationReason,
	hardDeadline time.Time,
) (*PlanActivityOutput, model.TokenUsage, error) {
	ctx := wfCtx.Context()
	req, reasonText, resumeOpts, err := r.prepareFinalizePlan(ctx, reg, input, base, allToolOutputs, caps, nextAttempt, turnID, reason)
	if err != nil {
		return nil, aggUsage, err
	}
	output, err := r.runPlanActivityRecovering(wfCtx, reg.ResumeActivityName, resumeOpts, req, hardDeadline, &caps, &nextAttempt)
	if err != nil {
		return nil, aggUsage, fmt.Errorf("%s: %w", reasonText, err)
	}
	if err := validateFinalizePlanOutput(output, reasonText); err != nil {
		return nil, aggUsage, err
	}
	combinedUsage, err := checkedAddTokenUsage(aggUsage, output.Usage)
	if err != nil {
		return nil, aggUsage, fmt.Errorf("aggregate finalizer model usage: %w", err)
	}
	aggUsage = combinedUsage
	if err := r.publishFinalizeOutput(ctx, base, input, turnID, output); err != nil {
		return nil, aggUsage, err
	}
	return output, aggUsage, nil
}

func validateFinalizePlanOutput(output *PlanActivityOutput, reasonText string) error {
	if output == nil || output.Result == nil {
		return fmt.Errorf("%s", reasonText)
	}
	if len(output.Result.ToolCalls) > 0 {
		if output.Result.FinalResponse != nil || output.Result.FinalToolResult != nil || output.Result.Await != nil {
			return fmt.Errorf("%s: finalization tool calls cannot be combined with terminal output or await", reasonText)
		}
		return nil
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
	caps policy.CapsState,
	nextAttempt int,
	turnID string,
	reason planner.TerminationReason,
) (PlanActivityInput, string, engine.ActivityOptions, error) {
	if err := r.publishFinalizingPhase(ctx, base, input, turnID); err != nil {
		return PlanActivityInput{}, "", engine.ActivityOptions{}, err
	}
	req, reasonText, err := r.buildFinalizePlanRequest(reg, base, input, allToolOutputs, caps, nextAttempt, reason)

	if err != nil {
		return PlanActivityInput{}, "", engine.ActivityOptions{}, err
	}
	if err := r.publishFinalizeTransition(ctx, base, input, turnID, reason); err != nil {
		return PlanActivityInput{}, "", engine.ActivityOptions{}, err
	}
	return req, reasonText, resolveResumeActivityOptions(reg, input), nil
}

func (r *Runtime) buildFinalizePlanRequest(
	reg AgentRegistration,
	base *planner.PlanInput,
	input *RunInput,
	allToolOutputs []*planner.ToolOutput,
	caps policy.CapsState,
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
		AgentID:          input.AgentID,
		RunID:            base.RunContext.RunID,
		Messages:         prepareFinalizationMessages(base.Messages, hint),
		RunContext:       resumeCtx,
		ToolOutputs:      encodedToolOutputs,
		Finalize:         &planner.Termination{Reason: reason, Message: hint},
		ToolPolicyActive: true,
		PolicyCaps:       caps,
	}
	req.AllowedTools = r.terminalFinalizerToolNames(reg, input)

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

func plannerResultNotes(result *planner.PlanResult) []planner.PlannerAnnotation {
	if result == nil {
		return nil
	}
	return result.Notes
}

func clonePlannerNotes(in []planner.PlannerAnnotation) []*planner.PlannerAnnotation {
	out := make([]*planner.PlannerAnnotation, len(in))
	for i := range in {
		out[i] = &in[i]
	}
	return out
}

func (r *Runtime) publishFinalizingPhase(ctx context.Context, base *planner.PlanInput, input *RunInput, turnID string) error {
	return r.publishHook(ctx, hooks.NewRunPhaseChangedEvent(base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, run.PhaseSynthesizing), turnID)
}

func finalizationHint(reason planner.TerminationReason) string {
	const terminalToolGuidance = "\n- Do NOT call domain tools. You may call an advertised terminal bookkeeping tool to produce the final result."
	switch reason {
	case planner.TerminationReasonTimeBudget:
		return "FINALIZE NOW: time budget reached.\n\n- Provide the best possible final answer using ONLY the information already available in the conversation and tool results." + terminalToolGuidance + "\n- Do NOT say you will call tools or that you will \"try\" another approach.\n- If additional domain tool calls would be needed, explain what you would have retrieved and how it would change the answer, then provide the best provisional answer."
	case planner.TerminationReasonToolCap:
		return "FINALIZE NOW: tool budget exhausted.\n\n- Provide the best possible final answer using ONLY the information already available in the conversation and tool results." + terminalToolGuidance + "\n- Do NOT say you will call tools.\n- If further domain tool calls would be needed, describe them briefly and provide the best provisional answer."
	case planner.TerminationReasonFailureCap:
		return "FINALIZE NOW: recovery budget exhausted.\n\n- Provide the best possible final answer using ONLY the information already available in the conversation and tool results." + terminalToolGuidance + "\n- Do NOT say you will call tools.\n- A model or tool result could not be accepted after bounded retries. State what remains uncertain, then provide the best provisional answer."
	default:
		return "FINALIZE NOW.\n\n- Provide the best possible final answer using ONLY the information already available in the conversation and tool results." + terminalToolGuidance + "\n- Do NOT say you will call tools.\n- If more work is needed, describe it succinctly and provide the best provisional answer."
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
		return "recovery turn cap exceeded"
	default:
		return "finalization failed"
	}
}

func (r *Runtime) publishFinalizationAssistantMessage(ctx context.Context, base *planner.PlanInput, input *RunInput, turnID string, msg *model.Message) error {
	if err := r.publishHook(ctx, hooks.NewAssistantMessageEvent(
		base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, agentMessageText(msg), nil,
	), turnID); err != nil {
		return err
	}
	return r.publishAssistantTurnCommitted(ctx, input, base, turnID, msg)
}
