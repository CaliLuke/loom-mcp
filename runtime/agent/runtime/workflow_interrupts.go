package runtime

// workflow_interrupts.go contains pause/resume helpers used by the plan/tool loop.
//
// Contract:
// - These helpers are deterministic and replay-safe: timeouts use workflow time.
// - Callers should only invoke them from within workflow execution.
// - The helpers publish lifecycle events via hooks so streams can close deterministically.

import (
	"context"
	"errors"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
)

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
