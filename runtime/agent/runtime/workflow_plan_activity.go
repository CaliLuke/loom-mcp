package runtime

// workflow_plan_activity.go contains plan-activity execution helpers used by the loop.
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

	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
)

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
