package runtime

import (
	"context"
	"fmt"

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

func (e *toolBatchExec) dispatchToolCalls(wfCtx engine.WorkflowContext, calls []planner.ToolRequest) (*toolCallBatch, error) {
	ctx := wfCtx.Context()
	b := newToolCallBatch(calls)

	for i, call := range calls {
		normalized, err := e.prepareToolDispatch(ctx, b, call, i)
		if err != nil {
			return nil, err
		}
		if normalized == nil {
			continue
		}
		if err := e.dispatchResolvedToolCall(ctx, wfCtx, b, *normalized, i); err != nil {
			return nil, err
		}
	}

	return b, nil
}

func newToolCallBatch(calls []planner.ToolRequest) *toolCallBatch {
	return &toolCallBatch{
		calls:         calls,
		futures:       make([]futureInfo, 0, len(calls)),
		childFutures:  make([]agentChildFutureInfo, 0, len(calls)),
		inlineByID:    make(map[string]*planner.ToolResult, len(calls)),
		discoveredIDs: make([]string, 0, len(calls)),
	}
}

func (e *toolBatchExec) prepareToolDispatch(ctx context.Context, b *toolCallBatch, call planner.ToolRequest, i int) (*planner.ToolRequest, error) {
	call = e.normalizeToolCall(call, i)
	b.calls[i] = call
	if _, hasSpec := e.r.toolSpec(call.Name); hasSpec {
		if err := e.publishToolCallScheduled(ctx, call, e.toolQueue(call.Name)); err != nil {
			return nil, err
		}
		return &call, nil
	}
	if err := e.dispatchUnknownToolCall(ctx, b, call); err != nil {
		return nil, err
	}
	return nil, nil
}

func (e *toolBatchExec) dispatchResolvedToolCall(ctx context.Context, wfCtx engine.WorkflowContext, b *toolCallBatch, call planner.ToolRequest, index int) error {
	spec, ts, hasTS := e.loadToolRegistration(call.Name)
	if hasTS && ts.Inline {
		return e.dispatchInlineResolvedToolCall(ctx, wfCtx, b, call, spec, ts, index)
	}
	return e.dispatchActivityToolCall(ctx, wfCtx, b, call, spec, hasTS, ts)
}

func (e *toolBatchExec) loadToolRegistration(name tools.Ident) (tools.ToolSpec, ToolsetRegistration, bool) {
	spec, _ := e.r.toolSpec(name)
	e.r.mu.RLock()
	ts, hasTS := e.r.toolsets[spec.Toolset]
	e.r.mu.RUnlock()
	return spec, ts, hasTS
}

func (e *toolBatchExec) dispatchInlineResolvedToolCall(ctx context.Context, wfCtx engine.WorkflowContext, b *toolCallBatch, call planner.ToolRequest, spec tools.ToolSpec, ts ToolsetRegistration, index int) error {
	call, err := e.adaptInlinePayload(ctx, call, ts)
	if err != nil {
		return err
	}
	b.calls[index] = call
	if spec.IsAgentTool {
		return e.dispatchInlineAgentToolCall(wfCtx, b, call, ts)
	}
	return e.dispatchInlineToolCall(wfCtx, b, call, ts)
}

func (e *toolBatchExec) dispatchUnknownToolCall(ctx context.Context, b *toolCallBatch, call planner.ToolRequest) error {
	if err := e.publishToolCallScheduled(ctx, call, ""); err != nil {
		return err
	}
	tr, err := e.synthesizeUnknownToolResult(ctx, call, 0)
	if err != nil {
		return err
	}
	b.inlineByID[call.ToolCallID] = tr
	e.recordDiscoveredToolCall(b, call.ToolCallID)
	return nil
}

func (e *toolBatchExec) adaptInlinePayload(ctx context.Context, call planner.ToolRequest, ts ToolsetRegistration) (planner.ToolRequest, error) {
	raw := call.Payload
	if ts.PayloadAdapter == nil || len(raw) == 0 {
		return call, nil
	}
	meta := ToolCallMeta{
		RunID:            call.RunID,
		SessionID:        call.SessionID,
		TurnID:           call.TurnID,
		ToolCallID:       call.ToolCallID,
		ParentToolCallID: call.ParentToolCallID,
	}
	adapted, err := ts.PayloadAdapter(ctx, meta, call.Name, raw.RawMessage())
	if err != nil {
		return call, fmt.Errorf("inline payload adapter failed for %s: %w", call.Name, err)
	}
	if len(adapted) == 0 {
		return call, nil
	}
	call.Payload = rawjson.Message(adapted)
	return call, nil
}

func (e *toolBatchExec) dispatchInlineToolCall(wfCtx engine.WorkflowContext, b *toolCallBatch, call planner.ToolRequest, ts ToolsetRegistration) error {
	start := wfCtx.Now()
	ctx := wfCtx.Context()
	ctxInline := engine.WithWorkflowContext(ctx, wfCtx)
	result, err := ts.Execute(ctxInline, &call)
	if err != nil {
		return fmt.Errorf("inline tool %q failed: %w", call.Name, err)
	}
	if result == nil {
		return fmt.Errorf("inline tool %q returned nil result", call.Name)
	}
	duration := wfCtx.Now().Sub(start)
	resultJSON, err := e.r.materializeToolResult(ctx, call, result)
	if err != nil {
		return err
	}
	if err := e.publishToolResultReceived(ctx, call, result, resultJSON, duration); err != nil {
		return err
	}
	b.inlineByID[call.ToolCallID] = result
	e.recordDiscoveredToolCall(b, call.ToolCallID)
	return nil
}

func (e *toolBatchExec) dispatchInlineAgentToolCall(wfCtx engine.WorkflowContext, b *toolCallBatch, call planner.ToolRequest, ts ToolsetRegistration) error {
	ctx := wfCtx.Context()
	messages, nestedRunCtx, err := e.r.buildAgentChildRequest(ctx, ts.AgentTool, &call, e.messages, e.runCtx)
	if err != nil {
		return e.recordInlineAgentRequestFailure(ctx, b, call, err)
	}
	if err := e.publishChildRunLinked(ctx, call, nestedRunCtx.RunID, ts.AgentTool.AgentID); err != nil {
		return err
	}
	route := ts.AgentTool.Route
	if route.ID == "" || route.WorkflowName == "" || route.DefaultTaskQueue == "" {
		return fmt.Errorf("agent tool route is incomplete for %s", call.Name)
	}
	input := buildInlineAgentRunInput(route.ID, nestedRunCtx, messages)
	handle, err := startInlineAgentChildWorkflow(wfCtx, ctx, route, &input)
	if err != nil {
		return fmt.Errorf("failed to start agent child workflow for %s: %w", call.Name, err)
	}
	b.childFutures = append(b.childFutures, agentChildFutureInfo{
		handle:    handle,
		call:      call,
		cfg:       ts.AgentTool,
		nestedRun: nestedRunCtx,
		startTime: wfCtx.Now(),
	})
	e.recordDiscoveredToolCall(b, call.ToolCallID)
	return nil
}

func (e *toolBatchExec) recordInlineAgentRequestFailure(ctx context.Context, b *toolCallBatch, call planner.ToolRequest, reqErr error) error {
	tr, resultErr := e.r.agentToolRequestFailureResult(call, reqErr)
	if resultErr != nil {
		return resultErr
	}
	if err := e.publishToolResultReceived(ctx, call, tr, nil, 0); err != nil {
		return err
	}
	b.inlineByID[call.ToolCallID] = tr
	e.recordDiscoveredToolCall(b, call.ToolCallID)
	return nil
}

func (e *toolBatchExec) publishChildRunLinked(ctx context.Context, call planner.ToolRequest, nestedRunID string, agentID agent.Ident) error {
	return e.r.publishHook(ctx, hooks.NewChildRunLinkedEvent(call.RunID, call.AgentID, call.SessionID, call.Name, call.ToolCallID, nestedRunID, agentID), "")
}

func buildInlineAgentRunInput(agentID agent.Ident, nestedRunCtx run.Context, messages []*model.Message) RunInput {
	return RunInput{
		AgentID:          agentID,
		RunID:            nestedRunCtx.RunID,
		SessionID:        nestedRunCtx.SessionID,
		TurnID:           nestedRunCtx.TurnID,
		ParentToolCallID: nestedRunCtx.ParentToolCallID,
		ParentRunID:      nestedRunCtx.ParentRunID,
		ParentAgentID:    nestedRunCtx.ParentAgentID,
		Tool:             nestedRunCtx.Tool,
		ToolArgs:         nestedRunCtx.ToolArgs,
		Labels:           nestedRunCtx.Labels,
		Messages:         messages,
	}
}

func startInlineAgentChildWorkflow(
	wfCtx engine.WorkflowContext,
	ctx context.Context,
	route AgentRoute,
	input *RunInput,
) (engine.ChildWorkflowHandle, error) {
	return wfCtx.StartChildWorkflow(ctx, engine.ChildWorkflowRequest{
		ID:        input.RunID,
		Workflow:  route.WorkflowName,
		TaskQueue: route.DefaultTaskQueue,
		Input:     input,
	})
}

func (e *toolBatchExec) dispatchActivityToolCall(ctx context.Context, wfCtx engine.WorkflowContext, b *toolCallBatch, call planner.ToolRequest, spec tools.ToolSpec, hasTS bool, ts ToolsetRegistration) error {
	toolInput := ToolInput{
		AgentID:          e.agentID,
		RunID:            e.runID,
		ToolsetName:      spec.Toolset,
		ToolName:         call.Name,
		ToolCallID:       call.ToolCallID,
		Payload:          call.Payload,
		SessionID:        call.SessionID,
		TurnID:           call.TurnID,
		ParentToolCallID: call.ParentToolCallID,
	}
	callOpts := computeToolActivityOptions(wfCtx, e.toolActOptions, e.finishBy)
	if callOpts.Queue == "" && hasTS && !ts.Inline && ts.TaskQueue != "" {
		callOpts.Queue = ts.TaskQueue
	}
	future, err := wfCtx.ExecuteToolActivityAsync(ctx, engine.ToolActivityCall{
		Name:    e.activityName,
		Input:   &toolInput,
		Options: callOpts,
	})
	if err != nil {
		return fmt.Errorf("failed to schedule tool %q: %w", call.Name, err)
	}
	b.futures = append(b.futures, futureInfo{
		future:    future,
		call:      call,
		startTime: wfCtx.Now(),
	})
	e.recordDiscoveredToolCall(b, call.ToolCallID)
	return nil
}

func (e *toolBatchExec) recordDiscoveredToolCall(b *toolCallBatch, toolCallID string) {
	if e.parentTracker == nil {
		return
	}
	b.discoveredIDs = append(b.discoveredIDs, toolCallID)
}

func (e *toolBatchExec) maybePublishChildTrackerUpdate(ctx context.Context, discoveredIDs []string) error {
	if e.parentTracker == nil || !e.parentTracker.registerDiscovered(discoveredIDs) || !e.parentTracker.needsUpdate() {
		return nil
	}
	if e.runCtx == nil || e.runCtx.ParentRunID == "" || e.runCtx.ParentAgentID == "" {
		return fmt.Errorf("nested tool tracker requires parent run context")
	}
	ev := hooks.NewToolCallUpdatedEvent(e.runCtx.ParentRunID, e.runCtx.ParentAgentID, e.sessionID, e.parentTracker.parentToolCallID, e.parentTracker.currentTotal())
	if err := e.r.publishHook(ctx, ev, e.turnID); err != nil {
		return err
	}
	e.parentTracker.markUpdated()
	return nil
}
