package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"goa.design/goa-ai/runtime/agent/engine"
	"goa.design/goa-ai/runtime/agent/planner"
	"goa.design/goa-ai/runtime/agent/tools"
)

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

func (e *toolBatchExec) collectActivityResultsAsComplete(wfCtx engine.WorkflowContext, futures []futureInfo, finalizeTimer engine.Future[time.Time]) (map[string]*planner.ToolResult, []futureInfo, bool, error) {
	ctx := wfCtx.Context()
	activityByID := make(map[string]*planner.ToolResult, len(futures))
	pending := append([]futureInfo(nil), futures...)
	for len(pending) > 0 {
		if err := waitForReadyActivityResult(wfCtx, ctx, pending, finalizeTimer); err != nil {
			return nil, nil, false, err
		}
		for {
			info, rest, ok := popReadyActivityFuture(pending)
			pending = rest
			if !ok {
				break
			}
			toolRes, err := e.collectActivityResult(wfCtx, ctx, info)
			if err != nil {
				return nil, nil, false, err
			}
			activityByID[info.call.ToolCallID] = toolRes
		}
		if finalizeTimer != nil && finalizeTimer.IsReady() && len(pending) > 0 {
			return activityByID, pending, true, nil
		}
	}
	return activityByID, nil, false, nil
}

func (e *toolBatchExec) collectAgentChildResults(wfCtx engine.WorkflowContext, children []agentChildFutureInfo, finalizeTimer engine.Future[time.Time]) (map[string]*planner.ToolResult, []agentChildFutureInfo, bool, error) {
	ctx := wfCtx.Context()
	if len(children) == 0 {
		return map[string]*planner.ToolResult{}, nil, false, nil
	}

	out := make(map[string]*planner.ToolResult, len(children))
	pending := append([]agentChildFutureInfo(nil), children...)
	for len(pending) > 0 {
		if err := waitForReadyChildResult(wfCtx, ctx, pending, finalizeTimer); err != nil {
			return nil, nil, false, err
		}
		for {
			info, rest, ok := popReadyChildFuture(pending)
			pending = rest
			if !ok {
				break
			}
			toolRes, err := e.collectChildResult(wfCtx, ctx, info)
			if err != nil {
				return nil, nil, false, err
			}
			out[info.call.ToolCallID] = toolRes
		}
		if finalizeTimer != nil && finalizeTimer.IsReady() && len(pending) > 0 {
			return out, pending, true, nil
		}
	}
	return out, nil, false, nil
}

func mergeToolResultsInCallOrder(calls []planner.ToolRequest, activityByID, inlineByID map[string]*planner.ToolResult) ([]*planner.ToolResult, error) {
	results := make([]*planner.ToolResult, 0, len(calls))
	for _, call := range calls {
		if ar, ok := activityByID[call.ToolCallID]; ok {
			results = append(results, ar)
			continue
		}
		if ir, ok := inlineByID[call.ToolCallID]; ok {
			results = append(results, ir)
			continue
		}
		return nil, fmt.Errorf("missing tool result for %q (%s)", call.Name, call.ToolCallID)
	}
	return results, nil
}

func waitForReadyActivityResult(wfCtx engine.WorkflowContext, ctx context.Context, pending []futureInfo, finalizeTimer engine.Future[time.Time]) error {
	return wfCtx.Await(ctx, func() bool {
		if finalizeTimer != nil && finalizeTimer.IsReady() {
			return true
		}
		for _, info := range pending {
			if info.future.IsReady() {
				return true
			}
		}
		return false
	})
}

func popReadyActivityFuture(pending []futureInfo) (futureInfo, []futureInfo, bool) {
	for i, info := range pending {
		if !info.future.IsReady() {
			continue
		}
		pending[i] = pending[len(pending)-1]
		return info, pending[:len(pending)-1], true
	}
	return futureInfo{}, pending, false
}

func (e *toolBatchExec) collectActivityResult(wfCtx engine.WorkflowContext, ctx context.Context, info futureInfo) (*planner.ToolResult, error) {
	out, err := info.future.Get(ctx)
	if err != nil {
		duration := wfCtx.Now().Sub(info.startTime)
		return e.synthesizeToolError(ctx, info.call, err, "tool activity failed", duration)
	}
	if out == nil {
		return nil, fmt.Errorf("tool %q returned nil output", info.call.Name)
	}
	duration := wfCtx.Now().Sub(info.startTime)
	if _, ok := e.r.toolSpec(info.call.Name); !ok {
		return e.synthesizeUnknownToolResult(ctx, info.call, duration)
	}
	toolRes, err := e.decodeActivityToolResult(ctx, info, out)
	if err != nil {
		return nil, err
	}
	if err := e.publishToolResultReceived(ctx, info.call, toolRes, out.Payload, duration); err != nil {
		return nil, err
	}
	return toolRes, nil
}

func (e *toolBatchExec) decodeActivityToolResult(ctx context.Context, info futureInfo, out *ToolOutput) (*planner.ToolResult, error) {
	spec, ok := e.r.toolSpec(info.call.Name)
	if !ok {
		return nil, fmt.Errorf("missing tool spec for %s", info.call.Name)
	}
	decoded, err := e.decodeActivityResultValue(ctx, info, out)
	if err != nil {
		return nil, err
	}
	toolRes := &planner.ToolResult{
		Name:       info.call.Name,
		Result:     decoded,
		Bounds:     out.Bounds,
		ServerData: out.ServerData,
		ToolCallID: info.call.ToolCallID,
		Telemetry:  out.Telemetry,
	}
	if out.Error != "" {
		toolRes.Error = planner.NewToolError(out.Error)
	}
	if err := e.r.enforceToolResultContracts(spec, info.call, toolRes); err != nil {
		return nil, err
	}
	applyActivityRetryHint(toolRes, spec, info.call, out)
	return toolRes, nil
}

func (e *toolBatchExec) decodeActivityResultValue(ctx context.Context, info futureInfo, out *ToolOutput) (any, error) {
	if out.Error != "" || !hasNonNullJSON(out.Payload.RawMessage()) {
		return nil, nil
	}
	v, err := e.r.unmarshalToolValue(ctx, info.call.Name, out.Payload.RawMessage(), false)
	if err != nil {
		return nil, fmt.Errorf("tool %q result decode failed (tool_call_id=%s): %w", info.call.Name, info.call.ToolCallID, err)
	}
	return v, nil
}

func applyActivityRetryHint(toolRes *planner.ToolResult, spec tools.ToolSpec, call planner.ToolRequest, out *ToolOutput) {
	if out.RetryHint == nil {
		return
	}
	h := *out.RetryHint
	if len(h.ExampleInput) == 0 && len(spec.Payload.ExampleInput) > 0 {
		h.ExampleInput = maps.Clone(spec.Payload.ExampleInput)
	}
	if len(h.PriorInput) == 0 && len(call.Payload) > 0 {
		var prior map[string]any
		if err := json.Unmarshal(call.Payload, &prior); err == nil && len(prior) > 0 {
			h.PriorInput = prior
		}
	}
	toolRes.RetryHint = &h
}

func waitForReadyChildResult(wfCtx engine.WorkflowContext, ctx context.Context, pending []agentChildFutureInfo, finalizeTimer engine.Future[time.Time]) error {
	return wfCtx.Await(ctx, func() bool {
		if finalizeTimer != nil && finalizeTimer.IsReady() {
			return true
		}
		for _, info := range pending {
			if info.handle.IsReady() {
				return true
			}
		}
		return false
	})
}

func popReadyChildFuture(pending []agentChildFutureInfo) (agentChildFutureInfo, []agentChildFutureInfo, bool) {
	for i, info := range pending {
		if !info.handle.IsReady() {
			continue
		}
		pending[i] = pending[len(pending)-1]
		return info, pending[:len(pending)-1], true
	}
	return agentChildFutureInfo{}, pending, false
}

func (e *toolBatchExec) collectChildResult(wfCtx engine.WorkflowContext, ctx context.Context, info agentChildFutureInfo) (*planner.ToolResult, error) {
	outPtr, err := info.handle.Get(wfCtx.Context())
	if err != nil {
		duration := wfCtx.Now().Sub(info.startTime)
		return e.synthesizeToolError(ctx, info.call, err, "agent tool execution failed", duration)
	}
	tr, err := e.r.adaptAgentChildOutput(ctx, info.cfg, &info.call, info.nestedRun, outPtr)
	if err != nil {
		return nil, err
	}
	duration := wfCtx.Now().Sub(info.startTime)
	if _, ok := e.r.toolSpec(info.call.Name); !ok {
		return e.synthesizeUnknownToolResult(ctx, info.call, duration)
	}
	resultJSON, err := e.r.materializeToolResult(ctx, info.call, tr)
	if err != nil {
		return nil, err
	}
	if err := e.publishToolResultReceived(ctx, info.call, tr, resultJSON, duration); err != nil {
		return nil, err
	}
	return tr, nil
}
