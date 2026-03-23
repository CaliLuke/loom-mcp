package runtime

// workflow_loop.go defines the internal loop context used by the durable plan/tool
// workflow implementation.
//
// The original workflow implementation threaded many values (workflow context,
// registration, run input/base, deadlines, activity options, interrupt controller,
// parent tracking, etc.) through long helper signatures. That style is brittle:
// it is easy to mis-thread values (e.g., budget vs hard deadline) and hard to
// evolve without propagating parameters everywhere.
//
// Contract:
// - workflowLoop is used only from within workflow execution (ExecuteWorkflow/runLoop).
// - It owns the shared, immutable context for a run iteration and provides helpers
//   that use workflow time for replay safety.
// - Mutable per-run state is held in runLoopState and is intentionally mutated in
//   place by loop methods.

import (
	"context"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/interrupt"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
)

type (
	workflowLoop struct {
		r *Runtime

		wfCtx engine.WorkflowContext
		reg   AgentRegistration

		input *RunInput
		base  *planner.PlanInput
		st    *runLoopState

		turnID        string
		ctrl          *interrupt.Controller
		parentTracker *childTracker
		deadlines     runDeadlines
		resumeOpts    engine.ActivityOptions
		toolOpts      engine.ActivityOptions
	}

	runDeadlines struct {
		// Budget is the run time budget deadline for internal work (planner resume,
		// tool execution, hooks). Time spent waiting on external input is explicitly
		// paused via (*runDeadlines).pause so an operator response does not burn the
		// run's time budget.
		Budget time.Time

		// Hard is the run deadline including finalizer grace. It is used to stop
		// scheduling new work when finalization cannot complete meaningfully.
		Hard time.Time

		// FinalizerGrace reserves time for finalization work (planner resume +
		// terminal hooks). When zero, callers should treat it as minActivityTimeout.
		FinalizerGrace time.Duration
	}
)

func newWorkflowLoop(
	r *Runtime,
	wfCtx engine.WorkflowContext,
	reg AgentRegistration,
	input *RunInput,
	base *planner.PlanInput,
	st *runLoopState,
	turnID string,
	ctrl *interrupt.Controller,
	parentTracker *childTracker,
	deadlines runDeadlines,
	resumeOpts engine.ActivityOptions,
	toolOpts engine.ActivityOptions,
) *workflowLoop {
	return &workflowLoop{
		r:             r,
		wfCtx:         wfCtx,
		reg:           reg,
		input:         input,
		base:          base,
		st:            st,
		turnID:        turnID,
		ctrl:          ctrl,
		parentTracker: parentTracker,
		deadlines:     deadlines,
		resumeOpts:    resumeOpts,
		toolOpts:      toolOpts,
	}
}

func (d runDeadlines) finalizeReserve() time.Duration {
	if d.FinalizerGrace > 0 {
		return d.FinalizerGrace
	}
	return minActivityTimeout
}

// pause extends the run deadlines by delta to account for time spent waiting on
// external input (clarifications, confirmations, UI-provided tool results).
//
// Contract:
// - delta must be derived from workflow time (wfCtx.Now()) so it is deterministic.
// - When deadlines are zero (no time budget configured), this is a no-op.
func (d *runDeadlines) pause(delta time.Duration) {
	if delta <= 0 {
		return
	}
	if !d.Budget.IsZero() {
		d.Budget = d.Budget.Add(delta)
	}
	if !d.Hard.IsZero() {
		d.Hard = d.Hard.Add(delta)
	}
}

// shouldFinalize reports whether it is too late to schedule new work and the runtime
// should move to finalization immediately.
func (d runDeadlines) shouldFinalize(now time.Time) bool {
	if d.Hard.IsZero() {
		return false
	}
	return d.Hard.Sub(now) <= d.finalizeReserve()
}

func (l *workflowLoop) run() (*RunOutput, error) {
	ctx := l.wfCtx.Context()
	for {
		if out, err := l.beforeTurn(ctx); err != nil || out != nil {
			return out, err
		}
		if out, handled, err := l.handleAwaitOnlyTurn(); err != nil {
			return nil, err
		} else if handled {
			if out != nil {
				return out, nil
			}
			continue
		}
		if out, done, err := l.handleToolOrFinish(ctx); err != nil {
			return nil, err
		} else if done {
			return out, nil
		}
	}
}

func (l *workflowLoop) beforeTurn(ctx context.Context) (*RunOutput, error) {
	if err := l.handlePendingInterrupts(); err != nil {
		return nil, err
	}
	if out, err := l.finalizeIfPastBudget(); err != nil || out != nil {
		return out, err
	}
	l.r.logger.Info(ctx, "Checking result.ToolCalls", "len", len(l.st.Result.ToolCalls))
	return nil, nil
}

func (l *workflowLoop) handleAwaitOnlyTurn() (*RunOutput, bool, error) {
	if l.st.Result.Await == nil || len(l.st.Result.ToolCalls) != 0 {
		return nil, false, nil
	}
	out, err := l.r.handleAwaitOnlyResult(
		l.wfCtx,
		l.reg,
		l.input,
		l.base,
		l.st,
		l.resumeOpts,
		l.ctrl,
		&l.deadlines,
		l.turnID,
	)
	return out, true, err
}

func (l *workflowLoop) handleToolOrFinish(ctx context.Context) (*RunOutput, bool, error) {
	if len(l.st.Result.ToolCalls) == 0 {
		l.r.logger.Info(ctx, "No tool calls, checking FinalResponse")
		out, err := l.r.finishWithoutToolCalls(ctx, l.input, l.base, l.st, l.turnID)
		return out, true, err
	}
	out, err := l.r.handleToolTurn(
		l.wfCtx,
		l.reg,
		l.input,
		l.base,
		l.st,
		l.resumeOpts,
		l.toolOpts,
		&l.deadlines,
		l.turnID,
		l.parentTracker,
		l.ctrl,
	)
	return out, out != nil, err
}

func (l *workflowLoop) handlePendingInterrupts() error {
	return l.r.handleInterrupts(
		l.wfCtx,
		l.input,
		l.base,
		l.turnID,
		l.ctrl,
		&l.st.NextAttempt,
		l.deadlines.Budget,
	)
}

func (l *workflowLoop) finalizeIfPastBudget() (*RunOutput, error) {
	now := l.wfCtx.Now()
	if l.deadlines.shouldFinalize(now) {
		return l.finalizeForTimeBudget()
	}
	if !l.deadlines.Hard.IsZero() && now.After(l.deadlines.Hard) {
		return l.finalizeForTimeBudget()
	}
	return nil, nil
}

func (l *workflowLoop) finalizeForTimeBudget() (*RunOutput, error) {
	return l.r.finalizeWithPlanner(
		l.wfCtx,
		l.reg,
		l.input,
		l.base,
		l.st.ToolEvents,
		l.st.ToolOutputs,
		l.st.AggUsage,
		l.st.NextAttempt,
		l.turnID,
		planner.TerminationReasonTimeBudget,
		l.deadlines.Hard,
	)
}
