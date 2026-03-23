package runtime

// workflow_finish.go contains “finish” helpers that translate a terminal planner
// result into the user-visible RunOutput and hook events.
//
// Contract:
// - These helpers must preserve the streaming semantics for streamed planners:
//   when the provider streamed content, the final message text may come from the
//   transcript rather than PlanResult.FinalResponse.Message.

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

// finishWithoutToolCalls finalizes a plan result when the planner returned no
// tool calls, producing the final assistant message and planner notes.
func (r *Runtime) finishWithoutToolCalls(
	ctx context.Context,
	input *RunInput,
	base *planner.PlanInput,
	st *runLoopState,
	turnID string,
) (*RunOutput, error) {
	result := st.Result
	if err := r.validateFinishResult(ctx, result); err != nil {
		return nil, err
	}
	finalMsg := finalPlannerMessage(&PlanActivityOutput{Result: result, Transcript: st.Transcript})
	if err := r.publishFinishArtifacts(ctx, input, base, turnID, result, finalMsg); err != nil {
		return nil, err
	}
	toolEvents, err := r.encodeToolEvents(ctx, st.ToolEvents)
	if err != nil {
		return nil, err
	}

	finalToolResult := finalToolResultEvent(base.RunContext.Tool, result.FinalToolResult)
	return &RunOutput{
		AgentID:         input.AgentID,
		RunID:           base.RunContext.RunID,
		Final:           finalMsg,
		FinalToolResult: finalToolResult,
		ToolEvents:      toolEvents,
		Notes:           clonePlannerNotes(result.Notes),
		Usage:           &st.AggUsage,
	}, nil
}

func (r *Runtime) validateFinishResult(ctx context.Context, result *planner.PlanResult) error {
	if err := validateTerminalPlanResult(result); err != nil {
		r.logger.Error(ctx, "ERROR - invalid planner terminal result", "err", err)
		return fmt.Errorf(
			"%w - ToolCalls=%d, FinalResponse=%v, FinalToolResult=%v, Await=%v",
			err,
			len(result.ToolCalls),
			result.FinalResponse != nil,
			result.FinalToolResult != nil,
			result.Await != nil,
		)
	}
	return nil
}

func (r *Runtime) publishFinishArtifacts(
	ctx context.Context,
	input *RunInput,
	base *planner.PlanInput,
	turnID string,
	result *planner.PlanResult,
	finalMsg *model.Message,
) error {
	if err := r.publishFinishAssistantMessage(ctx, input, base, turnID, result, finalMsg); err != nil {
		return err
	}
	return r.publishPlannerNotes(ctx, base, input, turnID, result.Notes)
}

func (r *Runtime) publishFinishAssistantMessage(
	ctx context.Context,
	input *RunInput,
	base *planner.PlanInput,
	turnID string,
	result *planner.PlanResult,
	finalMsg *model.Message,
) error {
	if result.FinalResponse == nil || result.Streamed {
		return nil
	}
	return r.publishHook(
		ctx,
		hooks.NewAssistantMessageEvent(
			base.RunContext.RunID,
			input.AgentID,
			base.RunContext.SessionID,
			agentMessageText(finalMsg),
			nil,
		),
		turnID,
	)
}

func validateTerminalPlanResult(result *planner.PlanResult) error {
	if result == nil {
		return fmt.Errorf("planner returned nil terminal result")
	}
	if result.FinalResponse == nil && result.FinalToolResult == nil {
		return fmt.Errorf("planner returned neither FinalResponse nor FinalToolResult")
	}
	if result.FinalResponse != nil && result.FinalToolResult != nil {
		return fmt.Errorf("planner returned both FinalResponse and FinalToolResult")
	}
	return nil
}

// finishAfterTerminalToolCalls completes the run after a tool turn whose executed
// tools are declared terminal (ToolSpec.TerminalRun). It returns a RunOutput with
// tool events but does not publish an assistant message event or request any
// follow-up PlanResume/finalization turn.
func (r *Runtime) finishAfterTerminalToolCalls(
	ctx context.Context,
	input *RunInput,
	base *planner.PlanInput,
	st *runLoopState,
) (*RunOutput, error) {
	toolEvents, err := r.encodeToolEvents(ctx, st.ToolEvents)
	if err != nil {
		return nil, err
	}
	return &RunOutput{
		AgentID:    input.AgentID,
		RunID:      base.RunContext.RunID,
		Final:      &model.Message{Role: model.ConversationRoleAssistant},
		ToolEvents: toolEvents,
		Usage:      &st.AggUsage,
	}, nil
}

// finalToolResultEvent converts the planner-owned final tool-result envelope
// into the workflow-safe api.ToolEvent shape stored on RunOutput.
func finalToolResultEvent(toolName tools.Ident, result *planner.FinalToolResult) *api.ToolEvent {
	if result == nil {
		return nil
	}
	return &api.ToolEvent{
		Name:                toolName,
		Result:              append(rawjson.Message(nil), result.Result...),
		ResultBytes:         result.ResultBytes,
		ResultOmitted:       result.ResultOmitted,
		ResultOmittedReason: result.ResultOmittedReason,
		ServerData:          append(rawjson.Message(nil), result.ServerData...),
		Bounds:              result.Bounds,
		Error:               result.Error,
		RetryHint:           result.RetryHint,
		Telemetry:           result.Telemetry,
	}
}
