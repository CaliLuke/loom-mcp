package runtime

// workflow_clarification.go contains the missing-field clarification flow.
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

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/policy"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

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
	base *workflowPlanInput,
	results []*planner.ToolResult,
	allResults []*planner.ToolResult,
	aggUsage model.TokenUsage,
	caps policy.CapsState,
	nextAttempt *int,
	turnID string,
	notes []planner.PlannerAnnotation,
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
		out, err := r.finalizeWithPlanner(wfCtx, reg, input, base, allResults, aggUsage, caps, *nextAttempt, turnID, notes, planner.TerminationReasonFailureCap, time.Time{})
		return out, err
	case MissingFieldsAwaitClarification:
		return r.awaitMissingFieldClarification(wfCtx, input, base, turnID, ctrl, deadlines, mf, triggerTool, triggerCall)
	case MissingFieldsResume:
		return nil, nil
	default:
		return nil, nil
	}
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
	base *workflowPlanInput,
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
	if err := appendClarificationAnswer(base, ans.AnswerRef); err != nil {
		return nil, err
	}
	if err := r.publishClarificationResumed(ctx, input, base, turnID, ans); err != nil {
		return nil, err
	}
	return nil, nil
}

func (r *Runtime) publishMissingFieldAwaitClarification(
	ctx context.Context,
	input *RunInput,
	base *workflowPlanInput,
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
	base *workflowPlanInput,
	turnID string,
	awaitID string,
) error {
	return r.publishHook(ctx, hooks.NewRunResumedEvent(
		base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, "clarification_error", "runtime",
		map[string]string{resumeMetadataKey: "clarification_error", "await_id": awaitID}, 0,
	), turnID)
}

func appendClarificationAnswer(base *workflowPlanInput, ref runstore.Hash) error {
	return base.appendEntry(index.MessageEntry{Kind: index.Clarification, Hash: ref})
}

func (r *Runtime) publishClarificationResumed(
	ctx context.Context,
	input *RunInput,
	base *workflowPlanInput,
	turnID string,
	ans interrupt.ClarificationAnswer,
) error {
	return r.publishHook(ctx, hooks.NewRunResumedEvent(
		base.RunContext.RunID, input.AgentID, base.RunContext.SessionID, "clarification_provided", input.RunID, ans.Labels, 1,
	), turnID)
}
