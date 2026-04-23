package runtime

import (
	"context"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
)

func (e *toolBatchExec) synthesizeExpiredBatchResults(ctx context.Context, calls []planner.ToolRequest) ([]*planner.ToolResult, error) {
	const cancelMsg = "canceled: time budget reached"
	results := make([]*planner.ToolResult, 0, len(calls))
	for i, call := range calls {
		call = e.normalizeToolCall(call, i)
		queue := e.toolQueue(call.Name)
		if err := e.publishToolCallScheduled(ctx, call, queue); err != nil {
			return nil, err
		}
		tr, err := e.newCanceledToolResult(ctx, call, cancelMsg)
		if err != nil {
			return nil, err
		}
		results = append(results, tr)
	}
	return results, nil
}

func (e *toolBatchExec) newCanceledToolResult(ctx context.Context, call planner.ToolRequest, message string) (*planner.ToolResult, error) {
	tr := &planner.ToolResult{
		Name:       call.Name,
		ToolCallID: call.ToolCallID,
		Error:      planner.NewToolError(message),
	}
	if _, ok := e.r.toolSpec(call.Name); ok {
		resultJSON, err := e.r.materializeToolResult(ctx, call, tr)
		if err != nil {
			return nil, err
		}
		if err := e.publishToolResultReceived(ctx, call, tr, resultJSON, 0); err != nil {
			return nil, err
		}
		return tr, nil
	}
	if err := e.publishToolResultReceived(ctx, call, tr, nil, 0); err != nil {
		return nil, err
	}
	return tr, nil
}

func (e *toolBatchExec) handleTimedOutBatch(wfCtx engine.WorkflowContext, ctx context.Context, timedOut bool, activityByID map[string]*planner.ToolResult, inlineByID map[string]*planner.ToolResult, pendingActs []futureInfo, pendingChildren []agentChildFutureInfo) error {
	if !timedOut {
		return nil
	}
	const cancelMsg = "canceled: time budget reached"
	if err := cancelPendingChildren(ctx, pendingChildren); err != nil {
		return err
	}
	if err := e.synthesizeTimedOutActivityResults(wfCtx, activityByID, pendingActs, cancelMsg); err != nil {
		return err
	}
	return e.synthesizeTimedOutChildResults(wfCtx, inlineByID, pendingChildren, cancelMsg)
}

func cancelPendingChildren(ctx context.Context, pendingChildren []agentChildFutureInfo) error {
	for _, info := range pendingChildren {
		if info.handle == nil {
			continue
		}
		if err := info.handle.Cancel(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (e *toolBatchExec) synthesizeTimedOutActivityResults(wfCtx engine.WorkflowContext, activityByID map[string]*planner.ToolResult, pending []futureInfo, cancelMsg string) error {
	for _, info := range pending {
		if info.call.ToolCallID == "" {
			continue
		}
		if _, ok := activityByID[info.call.ToolCallID]; ok {
			continue
		}
		tr, err := e.synthesizeTimeoutToolResult(wfCtx, info.call, info.startTime, cancelMsg)
		if err != nil {
			return err
		}
		activityByID[info.call.ToolCallID] = tr
	}
	return nil
}

func (e *toolBatchExec) synthesizeTimedOutChildResults(wfCtx engine.WorkflowContext, inlineByID map[string]*planner.ToolResult, pending []agentChildFutureInfo, cancelMsg string) error {
	for _, info := range pending {
		if info.call.ToolCallID == "" {
			continue
		}
		if _, ok := inlineByID[info.call.ToolCallID]; ok {
			continue
		}
		tr, err := e.synthesizeTimeoutToolResult(wfCtx, info.call, info.startTime, cancelMsg)
		if err != nil {
			return err
		}
		inlineByID[info.call.ToolCallID] = tr
	}
	return nil
}

func (e *toolBatchExec) synthesizeTimeoutToolResult(wfCtx engine.WorkflowContext, call planner.ToolRequest, startTime time.Time, cancelMsg string) (*planner.ToolResult, error) {
	ctx := wfCtx.Context()
	toolErr := planner.NewToolError(cancelMsg)
	tr := &planner.ToolResult{
		Name:       call.Name,
		ToolCallID: call.ToolCallID,
		Error:      toolErr,
	}
	duration := wfCtx.Now().Sub(startTime)
	if _, ok := e.r.toolSpec(call.Name); ok {
		resultJSON, err := e.r.materializeToolResult(ctx, call, tr)
		if err != nil {
			return nil, err
		}
		if err := e.publishToolResultReceived(ctx, call, tr, resultJSON, duration); err != nil {
			return nil, err
		}
		return tr, nil
	}
	if err := e.publishToolResultReceived(ctx, call, tr, nil, duration); err != nil {
		return nil, err
	}
	return tr, nil
}
