package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	agent "github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/session"
)

type runAdmissionCommand struct {
	Run         session.RunMeta
	ParentRunID string
}

const (
	reserveRunCommand            hooks.EventType = "runtime.reserve_run"
	rejectRunCommand             hooks.EventType = "runtime.reject_run"
	startRejectionCleanupTimeout                 = 10 * time.Second
)

// childStartAttemptID owns a deterministic child admission across workflow replay.
func childStartAttemptID(wfCtx engine.WorkflowContext, childID string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(wfCtx.RunID()+"\x00"+childID)))
}

// admitAgentChild commits child admission before publishing the parent link event.
func (r *Runtime) admitAgentChild(wfCtx engine.WorkflowContext, call planner.ToolRequest, child run.Context, agentID agent.Ident) error {
	if err := r.reserveChildRun(wfCtx, child, string(agentID)); err != nil {
		return err
	}
	return r.publishHook(wfCtx.Context(), hooks.NewChildRunLinkedEvent(call.RunID, call.AgentID, call.SessionID, call.Name, call.ToolCallID, child.RunID, agentID), "")
}

// reserveChildRun persists admission before starting a child, outside workflow code.
func (r *Runtime) reserveChildRun(wfCtx engine.WorkflowContext, child run.Context, agentID string) error {
	if child.SessionID == "" {
		return nil
	}
	payload, err := json.Marshal(runAdmissionCommand{
		Run:         session.RunMeta{RunID: child.RunID, AgentID: agentID, SessionID: child.SessionID, Status: session.RunStatusPending, Labels: cloneLabels(child.Labels)},
		ParentRunID: child.ParentRunID,
	})
	if err != nil {
		return err
	}
	return wfCtx.PublishHook(wfCtx.Context(), engine.HookActivityCall{
		Name:  hookActivityName,
		Input: &HookActivityInput{Type: reserveRunCommand, RunID: child.RunID, EventKey: childStartAttemptID(wfCtx, child.RunID), Payload: payload},
	})
}

// rejectChildStart settles only a proven rejection of this workflow's reservation.
func (r *Runtime) rejectChildStart(wfCtx engine.WorkflowContext, child run.Context, startErr error) error {
	if child.SessionID == "" || !errors.Is(startErr, engine.ErrWorkflowStartRejected) {
		return startErr
	}
	detached := wfCtx.Detached()
	err := detached.PublishHook(detached.Context(), engine.HookActivityCall{
		Name:  hookActivityName,
		Input: &HookActivityInput{Type: rejectRunCommand, RunID: child.RunID, EventKey: childStartAttemptID(wfCtx, child.RunID)},
	})
	if err != nil {
		return errors.Join(startErr, fmt.Errorf("settle rejected child start: %w", err))
	}
	return startErr
}

// rejectRunStart attempts bounded persistence even if the caller was canceled.
func (r *Runtime) rejectRunStart(ctx context.Context, runID, attemptID string, startErr error) error {
	if attemptID == "" || !errors.Is(startErr, engine.ErrWorkflowStartRejected) {
		return startErr
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), startRejectionCleanupTimeout)
	defer cancel()
	if err := r.SessionStore.RejectRun(cleanupCtx, runID, attemptID); err != nil {
		return errors.Join(startErr, fmt.Errorf("settle rejected workflow start: %w", err))
	}
	return startErr
}

// runAdmissionActivity handles private metadata commands without event publication.
func (r *Runtime) runAdmissionActivity(ctx context.Context, input *HookActivityInput) error {
	if input.Type == rejectRunCommand {
		return r.SessionStore.RejectRun(ctx, input.RunID, input.EventKey)
	}
	var command runAdmissionCommand
	if err := json.Unmarshal(input.Payload, &command); err != nil {
		return err
	}
	return r.SessionStore.ReserveRun(ctx, command.Run, input.EventKey, command.ParentRunID)
}
