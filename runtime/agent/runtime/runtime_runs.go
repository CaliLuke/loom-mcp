package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agent "goa.design/goa-ai/runtime/agent"
	"goa.design/goa-ai/runtime/agent/engine"
	"goa.design/goa-ai/runtime/agent/hooks"
	"goa.design/goa-ai/runtime/agent/interrupt"
	"goa.design/goa-ai/runtime/agent/model"
	"goa.design/goa-ai/runtime/agent/run"
	"goa.design/goa-ai/runtime/agent/runlog"
	"goa.design/goa-ai/runtime/agent/session"
)

// agentByID returns the registered agent by ID if present. The boolean indicates
// whether the agent was found. Intended for internal/runtime use and codegen.
func (r *Runtime) agentByID(id agent.Ident) (AgentRegistration, bool) {
	r.mu.RLock()
	agent, ok := r.agents[id]
	r.mu.RUnlock()
	return agent, ok
}

// ExecuteAgentChildWithRoute starts a provider agent as a child workflow using the
// explicit route metadata (workflow name and task queue). The child executes its own
// plan/execute loop and returns a RunOutput which is adapted by callers.
func (r *Runtime) ExecuteAgentChildWithRoute(
	wfCtx engine.WorkflowContext,
	route AgentRoute,
	messages []*model.Message,
	nestedRunCtx run.Context,
) (*RunOutput, error) {
	if route.ID == "" || route.WorkflowName == "" || route.DefaultTaskQueue == "" {
		return nil, fmt.Errorf("child route is incomplete")
	}
	input := RunInput{
		AgentID:          route.ID,
		RunID:            nestedRunCtx.RunID,
		SessionID:        nestedRunCtx.SessionID,
		TurnID:           nestedRunCtx.TurnID,
		ParentToolCallID: nestedRunCtx.ParentToolCallID,
		ParentRunID:      nestedRunCtx.ParentRunID,
		ParentAgentID:    nestedRunCtx.ParentAgentID,
		Tool:             nestedRunCtx.Tool,
		ToolArgs:         nestedRunCtx.ToolArgs,
		Messages:         messages,
		Labels:           nestedRunCtx.Labels,
	}
	handle, err := wfCtx.StartChildWorkflow(wfCtx.Context(), engine.ChildWorkflowRequest{
		ID:        input.RunID,
		Workflow:  route.WorkflowName,
		TaskQueue: route.DefaultTaskQueue,
		Input:     &input,
	})
	if err != nil {
		return nil, err
	}
	out, err := handle.Get(wfCtx.Context())
	if err != nil {
		return nil, err
	}
	return out, nil
}

// StartRun launches the agent workflow asynchronously and returns a workflow handle
// so callers can wait, signal, or cancel execution. The RunID is generated if not
// provided in the input. Returns an error if the agent is not registered or if the
// workflow fails to start.
func (r *Runtime) startRun(ctx context.Context, input *RunInput) (engine.WorkflowHandle, error) {
	if input.AgentID == "" {
		return nil, fmt.Errorf("%w: missing agent id", ErrAgentNotFound)
	}
	reg, ok := r.agentByID(input.AgentID)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrAgentNotFound, input.AgentID)
	}
	return r.startRunOn(ctx, input, reg.Workflow.Name, reg.Workflow.TaskQueue, true)
}

// startRunWithMeta launches the agent workflow using client-supplied metadata
// rather than a locally registered agent. This enables remote caller processes
// to start runs when workers are registered in another process.
func (r *Runtime) startRunWithRoute(ctx context.Context, input *RunInput, route AgentRoute) (engine.WorkflowHandle, error) {
	if route.ID == "" || route.WorkflowName == "" {
		return nil, fmt.Errorf("%w: missing route for agent client", ErrAgentNotFound)
	}
	if input.AgentID == "" {
		input.AgentID = route.ID
	}
	return r.startRunOn(ctx, input, route.WorkflowName, route.DefaultTaskQueue, true)
}

// startOneShotRun launches a one-shot workflow that does not belong to a session.
func (r *Runtime) startOneShotRun(ctx context.Context, input *RunInput) (engine.WorkflowHandle, error) {
	if input.AgentID == "" {
		return nil, fmt.Errorf("%w: missing agent id", ErrAgentNotFound)
	}
	reg, ok := r.agentByID(input.AgentID)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrAgentNotFound, input.AgentID)
	}
	return r.startRunOn(ctx, input, reg.Workflow.Name, reg.Workflow.TaskQueue, false)
}

// startOneShotRunWithRoute launches a one-shot workflow using client-supplied route metadata.
func (r *Runtime) startOneShotRunWithRoute(ctx context.Context, input *RunInput, route AgentRoute) (engine.WorkflowHandle, error) {
	if route.ID == "" || route.WorkflowName == "" {
		return nil, fmt.Errorf("%w: missing route for agent client", ErrAgentNotFound)
	}
	if input.AgentID == "" {
		input.AgentID = route.ID
	}
	return r.startRunOn(ctx, input, route.WorkflowName, route.DefaultTaskQueue, false)
}

// startRunOn contains common start logic for both locally-registered and
// remote-route clients.
func (r *Runtime) startRunOn(ctx context.Context, input *RunInput, workflowName, defaultQueue string, requireSession bool) (engine.WorkflowHandle, error) {
	if err := r.Seal(ctx); err != nil {
		return nil, err
	}
	if input.RunID == "" {
		input.RunID = generateRunID(string(input.AgentID))
	}
	if requireSession {
		if strings.TrimSpace(input.SessionID) == "" {
			return nil, ErrMissingSessionID
		}
		sess, err := r.SessionStore.LoadSession(ctx, input.SessionID)
		if err != nil {
			return nil, err
		}
		if sess.Status == session.StatusEnded {
			return nil, session.ErrSessionEnded
		}
	} else if strings.TrimSpace(input.SessionID) != "" {
		return nil, ErrSessionNotAllowed
	}
	reg, _ := r.agentByID(input.AgentID)
	req := engine.WorkflowStartRequest{
		ID:        input.RunID,
		Workflow:  workflowName,
		TaskQueue: defaultQueue,
		Input:     input,
	}
	req.RunTimeout = resolveRunTiming(reg, input).RunTimeout
	if opts := input.WorkflowOptions; opts != nil {
		if opts.TaskQueue != "" {
			req.TaskQueue = opts.TaskQueue
		}
		req.Memo = cloneMetadata(opts.Memo)
		req.SearchAttributes = cloneMetadata(opts.SearchAttributes)
		rp := engine.RetryPolicy{
			MaxAttempts:        opts.RetryPolicy.MaxAttempts,
			InitialInterval:    opts.RetryPolicy.InitialInterval,
			BackoffCoefficient: opts.RetryPolicy.BackoffCoefficient,
		}
		if !isZeroRetryPolicy(rp) {
			req.RetryPolicy = rp
		}
	}
	if requireSession {
		if v, ok := req.SearchAttributes["SessionID"]; ok && v != input.SessionID {
			return nil, fmt.Errorf("workflow search attribute SessionID=%v does not match session id %q", v, input.SessionID)
		}
		now := time.Now().UTC()
		if err := r.SessionStore.UpsertRun(ctx, session.RunMeta{
			AgentID:   string(input.AgentID),
			RunID:     input.RunID,
			SessionID: input.SessionID,
			Status:    session.RunStatusPending,
			StartedAt: now,
			UpdatedAt: now,
			Labels:    cloneLabels(input.Labels),
			Metadata:  cloneMetadata(input.Metadata),
		}); err != nil {
			return nil, err
		}
	} else if req.SearchAttributes != nil {
		if _, ok := req.SearchAttributes["SessionID"]; ok {
			return nil, fmt.Errorf("workflow search attribute SessionID is not allowed for one-shot runs")
		}
	}
	handle, err := r.Engine.StartWorkflow(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWorkflowStartFailed, err)
	}
	if r.RunEventStore != nil {
		handle = newObservedWorkflowHandle(r, input, handle)
	}
	r.storeWorkflowHandle(input.RunID, handle)
	return handle, nil
}

// CancelRun requests cancellation of the workflow identified by runID.
func (r *Runtime) CancelRun(ctx context.Context, runID string) error {
	if runID == "" {
		return errors.New("run id is required")
	}
	canceler, ok := r.Engine.(engine.Canceler)
	if !ok || canceler == nil {
		return fmt.Errorf("engine does not support cancel-by-id")
	}
	if err := canceler.CancelByID(ctx, runID); err != nil {
		if errors.Is(err, engine.ErrWorkflowNotFound) {
			return nil
		}
		return err
	}
	return nil
}

// PauseRun requests the underlying workflow to pause via the standard pause signal.
func (r *Runtime) PauseRun(ctx context.Context, req interrupt.PauseRequest) error {
	if req == nil {
		return errors.New("pause request is required")
	}
	if req.RunID == "" {
		return errors.New("run id is required")
	}
	if s, ok := r.Engine.(engine.Signaler); ok {
		return s.SignalByID(ctx, req.RunID, "", interrupt.SignalPause, req)
	}
	handle, ok := r.workflowHandle(req.RunID)
	if !ok {
		return fmt.Errorf("run %q not found", req.RunID)
	}
	return handle.Signal(ctx, interrupt.SignalPause, req)
}

// ResumeRun notifies the workflow that execution can continue.
func (r *Runtime) ResumeRun(ctx context.Context, req interrupt.ResumeRequest) error {
	if req == nil {
		return errors.New("resume request is required")
	}
	if req.RunID == "" {
		return errors.New("run id is required")
	}
	if s, ok := r.Engine.(engine.Signaler); ok {
		return s.SignalByID(ctx, req.RunID, "", interrupt.SignalResume, req)
	}
	handle, ok := r.workflowHandle(req.RunID)
	if !ok {
		return fmt.Errorf("run %q not found", req.RunID)
	}
	return handle.Signal(ctx, interrupt.SignalResume, req)
}

// ProvideClarification sends a typed clarification answer to a waiting run.
func (r *Runtime) ProvideClarification(ctx context.Context, ans interrupt.ClarificationAnswer) error {
	if ans == nil {
		return errors.New("clarification answer is required")
	}
	if ans.RunID == "" {
		return errors.New("run id is required")
	}
	if s, ok := r.Engine.(engine.Signaler); ok {
		return r.mapAwaitSignalError(ctx, ans.RunID, s.SignalByID(ctx, ans.RunID, "", interrupt.SignalProvideClarification, ans))
	}
	handle, ok := r.workflowHandle(ans.RunID)
	if !ok {
		return r.mapAwaitSignalError(ctx, ans.RunID, engine.ErrWorkflowNotFound)
	}
	return r.mapAwaitSignalError(ctx, ans.RunID, handle.Signal(ctx, interrupt.SignalProvideClarification, ans))
}

// ProvideToolResults sends a set of external tool results to a waiting run.
func (r *Runtime) ProvideToolResults(ctx context.Context, rs interrupt.ToolResultsSet) error {
	if rs == nil {
		return errors.New("tool results set is required")
	}
	if rs.RunID == "" {
		return errors.New("run id is required")
	}
	if s, ok := r.Engine.(engine.Signaler); ok {
		return r.mapAwaitSignalError(ctx, rs.RunID, s.SignalByID(ctx, rs.RunID, "", interrupt.SignalProvideToolResults, rs))
	}
	handle, ok := r.workflowHandle(rs.RunID)
	if !ok {
		return r.mapAwaitSignalError(ctx, rs.RunID, engine.ErrWorkflowNotFound)
	}
	return r.mapAwaitSignalError(ctx, rs.RunID, handle.Signal(ctx, interrupt.SignalProvideToolResults, rs))
}

// ProvideConfirmation sends a typed confirmation decision to a waiting run.
func (r *Runtime) ProvideConfirmation(ctx context.Context, dec interrupt.ConfirmationDecision) error {
	if dec == nil {
		return errors.New("confirmation decision is required")
	}
	if strings.TrimSpace(dec.RunID) == "" {
		return errors.New("run id is required")
	}
	if s, ok := r.Engine.(engine.Signaler); ok {
		return r.mapAwaitSignalError(ctx, dec.RunID, s.SignalByID(ctx, dec.RunID, "", interrupt.SignalProvideConfirmation, dec))
	}
	handle, ok := r.workflowHandle(dec.RunID)
	if !ok {
		return r.mapAwaitSignalError(ctx, dec.RunID, engine.ErrWorkflowNotFound)
	}
	return r.mapAwaitSignalError(ctx, dec.RunID, handle.Signal(ctx, interrupt.SignalProvideConfirmation, dec))
}

// mapAwaitSignalError converts engine signal-delivery errors into typed runtime await-resume errors.
func (r *Runtime) mapAwaitSignalError(ctx context.Context, runID string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, engine.ErrWorkflowCompleted) {
		return &RunNotAwaitableError{
			RunID:  runID,
			Reason: RunNotAwaitableCompletedRun,
			Cause:  err,
		}
	}
	if !errors.Is(err, engine.ErrWorkflowNotFound) {
		return err
	}

	status, statusErr := r.Engine.QueryRunStatus(ctx, runID)
	if statusErr == nil {
		if isTerminalRunStatus(status) {
			return &RunNotAwaitableError{
				RunID:  runID,
				Reason: RunNotAwaitableCompletedRun,
				Cause:  err,
			}
		}
		return err
	}
	if errors.Is(statusErr, engine.ErrWorkflowNotFound) {
		return &RunNotAwaitableError{
			RunID:  runID,
			Reason: RunNotAwaitableUnknownRun,
			Cause:  err,
		}
	}
	return fmt.Errorf("query run status after signal failure: %w", statusErr)
}

// isTerminalRunStatus reports whether the run lifecycle is permanently closed.
func isTerminalRunStatus(status engine.RunStatus) bool {
	switch status {
	case engine.RunStatusCompleted, engine.RunStatusTimedOut, engine.RunStatusFailed, engine.RunStatusCanceled:
		return true
	case engine.RunStatusPending, engine.RunStatusRunning, engine.RunStatusPaused:
		return false
	}
	return false
}

// ListRunEvents returns a forward page of canonical run events for the given run.
func (r *Runtime) ListRunEvents(ctx context.Context, runID, cursor string, limit int) (runlog.Page, error) {
	page, err := r.RunEventStore.List(ctx, runID, cursor, limit)
	if err != nil {
		return runlog.Page{}, err
	}
	if !runEventPageNeedsTerminalRepair(page) {
		return page, nil
	}
	if err := r.repairTerminalRunCompletion(ctx, runID); err != nil {
		r.logWarn(ctx, "run completion repair skipped for event read", err, "run_id", runID)
		return page, nil
	}
	repaired, err := r.RunEventStore.List(ctx, runID, cursor, limit)
	if err != nil {
		return runlog.Page{}, err
	}
	if !repairedTailNeedsCompletionDelta(page, repaired) {
		return repaired, nil
	}
	delta, err := r.RunEventStore.List(ctx, runID, page.Events[len(page.Events)-1].ID, 1)
	if err != nil {
		return runlog.Page{}, err
	}
	if len(delta.Events) == 0 || delta.Events[0].Type != hooks.RunCompleted {
		return repaired, nil
	}
	events := append([]*runlog.Event(nil), page.Events...)
	events = append(events, delta.Events[0])
	return runlog.Page{
		Events:     events,
		NextCursor: delta.NextCursor,
	}, nil
}

// GetRunSnapshot derives a compact snapshot of the run state by replaying the canonical run log.
func (r *Runtime) GetRunSnapshot(ctx context.Context, runID string) (*run.Snapshot, error) {
	snapshot, err := r.loadRunSnapshot(ctx, runID)
	if err != nil {
		if errors.Is(err, run.ErrNotFound) {
			if repairErr := r.repairTerminalRunCompletion(ctx, runID); repairErr != nil {
				r.logWarn(ctx, "run completion repair skipped for missing snapshot", repairErr, "run_id", runID)
				return nil, err
			}
			return r.loadRunSnapshot(ctx, runID)
		}
		return nil, err
	}
	if snapshot.Status == run.StatusCompleted ||
		snapshot.Status == run.StatusFailed ||
		snapshot.Status == run.StatusCanceled {
		return snapshot, nil
	}
	if err := r.repairTerminalRunCompletion(ctx, runID); err != nil {
		r.logWarn(ctx, "run completion repair skipped for snapshot read", err, "run_id", runID)
		return snapshot, nil
	}
	return r.loadRunSnapshot(ctx, runID)
}

// loadRunSnapshot replays the canonical run log without attempting terminal repair.
func (r *Runtime) loadRunSnapshot(ctx context.Context, runID string) (*run.Snapshot, error) {
	const pageSize = 512

	var (
		cursor = ""
		events []*runlog.Event
	)
	for {
		page, err := r.RunEventStore.List(ctx, runID, cursor, pageSize)
		if err != nil {
			return nil, err
		}
		events = append(events, page.Events...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return newRunSnapshot(events)
}

// runEventPageNeedsTerminalRepair reports whether a caller is currently reading
// the durable tail of the run log without a canonical RunCompleted event.
func runEventPageNeedsTerminalRepair(page runlog.Page) bool {
	if page.NextCursor != "" {
		return false
	}
	if len(page.Events) == 0 {
		return true
	}
	return page.Events[len(page.Events)-1].Type != hooks.RunCompleted
}

// repairedTailNeedsCompletionDelta reports whether repair appended a terminal
// event behind an originally full tail page.
func repairedTailNeedsCompletionDelta(original, repaired runlog.Page) bool {
	if len(original.Events) == 0 || len(repaired.Events) == 0 {
		return false
	}
	if repaired.NextCursor == "" {
		return false
	}
	return repaired.Events[len(repaired.Events)-1].Type != hooks.RunCompleted
}
