package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

func (e *toolBatchExec) collectActivityResultsAsComplete(wfCtx engine.WorkflowContext, futures []futureInfo, finalizeTimer engine.Future[time.Time]) (map[string]*ToolExecutionResult, []futureInfo, bool, error) {
	ctx := wfCtx.Context()
	activityByID := make(map[string]*ToolExecutionResult, len(futures))
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
			exec, err := e.collectActivityExecution(wfCtx, ctx, info)
			if err != nil {
				return nil, nil, false, err
			}
			activityByID[info.call.ToolCallID] = exec
		}
		if finalizeTimer != nil && finalizeTimer.IsReady() && len(pending) > 0 {
			return activityByID, pending, true, nil
		}
	}
	return activityByID, nil, false, nil
}

func (e *toolBatchExec) collectAgentChildResults(wfCtx engine.WorkflowContext, children []agentChildFutureInfo, finalizeTimer engine.Future[time.Time]) (map[string]*ToolExecutionResult, []agentChildFutureInfo, bool, error) {
	ctx := wfCtx.Context()
	if len(children) == 0 {
		return map[string]*ToolExecutionResult{}, nil, false, nil
	}

	out := make(map[string]*ToolExecutionResult, len(children))
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
			out[info.call.ToolCallID] = Executed(toolRes)
		}
		if finalizeTimer != nil && finalizeTimer.IsReady() && len(pending) > 0 {
			return out, pending, true, nil
		}
	}
	return out, nil, false, nil
}

func mergeToolResultsInCallOrder(calls []planner.ToolRequest, activityByID, inlineByID map[string]*ToolExecutionResult) ([]*ToolExecutionResult, error) {
	results := make([]*ToolExecutionResult, 0, len(calls))
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

// collectActivityExecution adapts collectActivityResult into a ToolExecutionResult,
// threading the runtime-owned pause signal through from the activity output.
func (e *toolBatchExec) collectActivityExecution(wfCtx engine.WorkflowContext, ctx context.Context, info futureInfo) (*ToolExecutionResult, error) {
	out, err := info.future.Get(ctx)
	if err != nil {
		failure := inspectCollectedExecutionFailure(err)
		if failure.containsCancellation {
			return nil, failure.err
		}
		duration := wfCtx.Now().Sub(info.startTime)
		tr, synthErr := e.synthesizeToolError(ctx, info.call, failure, "tool activity failed", duration)
		if synthErr != nil {
			return nil, synthErr
		}
		return Executed(tr), nil
	}
	if out == nil {
		return nil, fmt.Errorf("tool %q returned nil output", info.call.Name)
	}
	duration := wfCtx.Now().Sub(info.startTime)
	if _, ok := e.r.toolSpec(info.call.Name); !ok {
		tr, synthErr := e.synthesizeUnknownToolResult(ctx, info.call, duration)
		if synthErr != nil {
			return nil, synthErr
		}
		return Executed(tr), nil
	}
	toolRes, err := e.decodeActivityToolResult(ctx, info, out)
	if err != nil {
		return nil, err
	}
	if err := validateToolPauseContract(info.call, toolRes, out.Pause); err != nil {
		return nil, err
	}
	if err := e.publishToolResultReceived(ctx, info.call, toolRes, out.Payload, duration); err != nil {
		return nil, err
	}
	return &ToolExecutionResult{ToolResult: toolRes, Pause: out.Pause}, nil
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
		Artifacts:  artifactContentsFromRefs(out.Artifacts),
	}
	if out.Error != "" {
		toolRes.Error = planner.NewToolError(out.Error)
	}
	if err := e.r.enforceToolResultContracts(spec, info.call, toolRes); err != nil {
		return nil, err
	}
	applyActivityRetryHint(toolRes, spec, out)
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

func applyActivityRetryHint(toolRes *planner.ToolResult, spec tools.ToolSpec, out *ToolOutput) {
	if out.RetryHint == nil {
		return
	}
	h := *out.RetryHint
	h.ExampleInput = nil
	h.PriorInput = nil
	h.Message = appendFieldContract(h.Message, generatedFieldContract(spec))
	toolRes.RetryHint = BoundGeneratedRetryHint(&h)
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
		failure := inspectCollectedExecutionFailure(err)
		if failure.containsCancellation {
			return nil, failure.err
		}
		duration := wfCtx.Now().Sub(info.startTime)
		return e.synthesizeToolError(ctx, info.call, failure, "agent tool execution failed", duration)
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
