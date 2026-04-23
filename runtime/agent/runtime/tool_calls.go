package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	agent "github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
	goa "github.com/CaliLuke/loom/pkg"
)

type (
	// futureInfo bundles a Future with its associated tool call metadata for parallel execution.
	// When tools are launched asynchronously via ExecuteToolActivityAsync, we need to track the
	// future handle alongside the original call details and start time so we can correlate
	// results and measure duration when collecting completed activities.
	futureInfo struct {
		// future is the typed engine Future for this tool call.
		future engine.Future[*ToolOutput]
		// call is the original tool request that was submitted for execution.
		call planner.ToolRequest
		// startTime records when the activity was scheduled, used to calculate tool duration.
		startTime time.Time
	}

	// agentChildFutureInfo bundles a child workflow handle with its associated
	// agent-as-tool call metadata so the runtime can fan in results after
	// concurrent child execution.
	agentChildFutureInfo struct {
		// handle is the child workflow handle returned by StartChildWorkflow.
		handle engine.ChildWorkflowHandle
		// call is the original agent-as-tool request submitted for execution.
		call planner.ToolRequest
		// cfg carries the agent-tool configuration used to adapt RunOutput.
		cfg *AgentToolConfig
		// nestedRun describes the nested agent run context (run IDs, parents).
		nestedRun run.Context
		// startTime records when the child workflow was started.
		startTime time.Time
	}

	// toolCallBatch carries the in-flight execution state for a batch of tool calls.
	//
	// The batch is constructed during dispatch (scheduling activities, starting agent
	// child workflows, and executing inline toolsets) and then consumed during
	// collection to merge results deterministically in the original call order.
	toolCallBatch struct {
		calls []planner.ToolRequest

		futures      []futureInfo
		childFutures []agentChildFutureInfo
		inlineByID   map[string]*planner.ToolResult

		discoveredIDs []string
	}

	// toolBatchExec bundles the common execution context shared by the helpers in this file.
	//
	// This exists to keep function signatures and call sites small and readable:
	// the batch execution flow is conceptually a single operation, but it needs a
	// lot of shared metadata (run IDs, timers) to be
	// propagated consistently to hooks and result contracts.
	toolBatchExec struct {
		r *Runtime

		activityName   string
		toolActOptions engine.ActivityOptions

		runID     string
		agentID   agent.Ident
		sessionID string
		turnID    string
		runCtx    *run.Context
		messages  []*model.Message

		expectedChildren int
		parentTracker    *childTracker
		finishBy         time.Time
	}
)

// collectToolCallIDs returns the tool call IDs in the same order as calls.
func collectToolCallIDs(calls []planner.ToolRequest) []string {
	ids := make([]string, 0, len(calls))
	for _, call := range calls {
		ids = append(ids, call.ToolCallID)
	}
	return ids
}

func (e *toolBatchExec) normalizeToolCall(call planner.ToolRequest, i int) planner.ToolRequest {
	call = e.applyDefaultToolCallContext(call)
	call = e.assignToolCallID(call, i)
	call = e.inheritParentToolCallID(call)
	return call
}

func (e *toolBatchExec) applyDefaultToolCallContext(call planner.ToolRequest) planner.ToolRequest {
	if call.SessionID == "" {
		call.SessionID = e.sessionID
	}
	if len(call.Labels) == 0 && e.runCtx != nil && len(e.runCtx.Labels) > 0 {
		call.Labels = cloneLabels(e.runCtx.Labels)
	}
	if call.TurnID == "" {
		call.TurnID = e.turnID
	}
	return call
}

func (e *toolBatchExec) assignToolCallID(call planner.ToolRequest, i int) planner.ToolRequest {
	if call.ToolCallID != "" {
		return call
	}
	attempt := 0
	if e.runCtx != nil {
		attempt = e.runCtx.Attempt
	}
	call.ToolCallID = generateDeterministicToolCallID(e.runID, call.TurnID, attempt, call.Name, i)
	return call
}

func (e *toolBatchExec) inheritParentToolCallID(call planner.ToolRequest) planner.ToolRequest {
	if call.ParentToolCallID != "" {
		return call
	}
	if e.parentTracker != nil {
		call.ParentToolCallID = e.parentTracker.parentToolCallID
	}
	if call.ParentToolCallID == "" && e.runCtx != nil && e.runCtx.ParentToolCallID != "" {
		call.ParentToolCallID = e.runCtx.ParentToolCallID
	}
	return call
}

func parentToolCallID(call planner.ToolRequest, runCtx *run.Context) string {
	if call.ParentToolCallID != "" {
		return call.ParentToolCallID
	}
	if runCtx != nil {
		return runCtx.ParentToolCallID
	}
	return ""
}

func retryHintFromExecutionError(tool tools.Ident, err error) *planner.RetryHint {
	var svcErr *goa.ServiceError
	if errors.As(err, &svcErr) && svcErr.Name == "service_unavailable" {
		return &planner.RetryHint{
			Reason: planner.RetryReasonToolUnavailable,
			Tool:   tool,
			Message: "Tool execution failed because the provider is temporarily unavailable. " +
				"Retry the same tool call with the same payload.",
		}
	}
	return nil
}

// synthesizeToolError creates a ToolResult from an execution error and publishes
// the corresponding ToolResultReceived event. This is used when activity or
// child workflow execution fails (e.g., timeout) and we want to convert the
// error into a tool result rather than failing the workflow.
func (e *toolBatchExec) synthesizeToolError(ctx context.Context, call planner.ToolRequest, err error, errMsg string, duration time.Duration) (*planner.ToolResult, error) {
	toolErr := planner.NewToolErrorWithCause(errMsg, err)
	toolRes := &planner.ToolResult{
		Name:       call.Name,
		ToolCallID: call.ToolCallID,
		Error:      toolErr,
		RetryHint:  retryHintFromExecutionError(call.Name, err),
	}
	if _, ok := e.r.toolSpec(call.Name); !ok {
		return e.synthesizeUnknownToolResult(ctx, call, duration)
	}
	resultJSON, err := e.r.materializeToolResult(ctx, call, toolRes)
	if err != nil {
		return nil, err
	}
	if err := e.publishToolResultReceived(ctx, call, toolRes, resultJSON, duration); err != nil {
		return nil, err
	}
	return toolRes, nil
}

// synthesizeUnknownToolResult converts an unregistered tool call into a tool error result.
//
// Provider adapters may surface hallucinated tool names (for example, when a model
// echoes a tool it saw in prior context but that was not advertised in the current
// request). This must not fail the workflow: the runtime returns a tool result error
// with a RetryHint so the planner can resume and the model can recover.
func (e *toolBatchExec) synthesizeUnknownToolResult(ctx context.Context, call planner.ToolRequest, duration time.Duration) (*planner.ToolResult, error) {
	toolErr := planner.NewToolError(fmt.Sprintf("unknown tool %q", call.Name))
	tr := &planner.ToolResult{
		Name:       call.Name,
		ToolCallID: call.ToolCallID,
		Error:      toolErr,
		RetryHint: &planner.RetryHint{
			Reason:         planner.RetryReasonToolUnavailable,
			Tool:           call.Name,
			RestrictToTool: false,
			Message:        "Tool name is not registered for this run. Choose a tool from the advertised tool list and call it with the exact JSON schema.",
		},
	}
	if err := e.publishToolResultReceived(ctx, call, tr, nil, duration); err != nil {
		return nil, err
	}
	return tr, nil
}

func (r *Runtime) enforceToolResultContracts(spec tools.ToolSpec, call planner.ToolRequest, tr *planner.ToolResult) error {
	return validateToolResultContract(spec, call, tr)
}

func (e *toolBatchExec) publishToolResultReceived(ctx context.Context, call planner.ToolRequest, tr *planner.ToolResult, resultJSON rawjson.Message, duration time.Duration) error {
	parentID := parentToolCallID(call, e.runCtx)
	ev := hooks.NewToolResultReceivedEvent(
		e.runID,
		e.agentID,
		e.sessionID,
		call.Name,
		call.ToolCallID,
		parentID,
		tr.Result,
		resultJSON,
		tr.ServerData,
		formatResultPreview(call.Name, tr.Result, tr.Bounds),
		tr.Bounds,
		duration,
		tr.Telemetry,
		tr.RetryHint,
		tr.Error,
	)
	return e.r.publishHook(ctx, ev, e.turnID)
}

func (e *toolBatchExec) publishToolCallScheduled(ctx context.Context, call planner.ToolRequest, queue string) error {
	ev := hooks.NewToolCallScheduledEvent(e.runID, e.agentID, e.sessionID, call.Name, call.ToolCallID, call.Payload, queue, call.ParentToolCallID, e.expectedChildren)
	return e.r.publishHook(ctx, ev, e.turnID)
}

func computeToolActivityOptions(wfCtx engine.WorkflowContext, base engine.ActivityOptions, finishBy time.Time) engine.ActivityOptions {
	callOpts := base
	startToClose, scheduleToStart := clampActivityTimeouts(wfCtx, base, finishBy)
	callOpts.StartToCloseTimeout = startToClose
	callOpts.ScheduleToStartTimeout = scheduleToStart
	return callOpts
}

func clampActivityTimeouts(wfCtx engine.WorkflowContext, base engine.ActivityOptions, finishBy time.Time) (time.Duration, time.Duration) {
	startToClose := base.StartToCloseTimeout
	scheduleToStart := base.ScheduleToStartTimeout
	if finishBy.IsZero() {
		return startToClose, scheduleToStart
	}
	if rem := finishBy.Sub(wfCtx.Now()); rem > 0 {
		startToClose = minNonZeroDuration(startToClose, rem)
		scheduleToStart = minNonZeroDuration(scheduleToStart, rem)
	}
	return startToClose, scheduleToStart
}

func minNonZeroDuration(current, candidate time.Duration) time.Duration {
	if current == 0 || current > candidate {
		return candidate
	}
	return current
}

// executeToolCalls schedules tool execution (inline, activity, and agent-as-tool child workflows)
// and collects results.
//
// The runtime publishes ToolCallScheduled events in call order, then publishes
// ToolResultReceived events as individual tool executions complete (not necessarily in
// call order). The returned results slice is always merged deterministically in the
// original call order so downstream planner/finalizer behavior remains stable.
//
// expectedChildren indicates how many child tools are expected to be discovered dynamically
// by the tools in this batch (0 if not tracked).
func (r *Runtime) executeToolCalls(wfCtx engine.WorkflowContext, activityName string, toolActOptions engine.ActivityOptions, agentID agent.Ident, runCtx *run.Context, messages []*model.Message, calls []planner.ToolRequest, expectedChildren int, parentTracker *childTracker, finishBy time.Time) ([]*planner.ToolResult, bool, error) {
	if runCtx == nil {
		return nil, false, fmt.Errorf("missing run context")
	}
	exec := &toolBatchExec{
		r:                r,
		activityName:     activityName,
		toolActOptions:   toolActOptions,
		runID:            runCtx.RunID,
		agentID:          agentID,
		sessionID:        runCtx.SessionID,
		turnID:           runCtx.TurnID,
		runCtx:           runCtx,
		messages:         messages,
		expectedChildren: expectedChildren,
		parentTracker:    parentTracker,
		finishBy:         finishBy,
	}

	ctx := wfCtx.Context()
	if exec.timeBudgetExpired(wfCtx) {
		results, err := exec.synthesizeExpiredBatchResults(ctx, calls)
		return results, true, err
	}

	execWfCtx, cancelExec := wfCtx.WithCancel()
	execCanceled := false
	cancelExecOnce := func() {
		if execCanceled {
			return
		}
		execCanceled = true
		if cancelExec != nil {
			cancelExec()
		}
	}

	finalizeTimer, err := exec.newFinalizeTimer(ctx, wfCtx)
	if err != nil {
		return nil, false, err
	}
	return r.runToolBatch(wfCtx, execWfCtx, ctx, exec, calls, finalizeTimer, cancelExecOnce)
}

func (r *Runtime) runToolBatch(wfCtx engine.WorkflowContext, execWfCtx engine.WorkflowContext, ctx context.Context, exec *toolBatchExec, calls []planner.ToolRequest, finalizeTimer engine.Future[time.Time], cancelExecOnce func()) ([]*planner.ToolResult, bool, error) {
	batch, err := exec.dispatchToolCalls(execWfCtx, calls)
	if err != nil {
		cancelExecOnce()
		return nil, false, err
	}
	if err := exec.maybePublishChildTrackerUpdate(ctx, batch.discoveredIDs); err != nil {
		cancelExecOnce()
		return nil, false, err
	}

	activityByID, pendingActs, timedOutActs, err := exec.collectActivityResultsAsComplete(wfCtx, batch.futures, finalizeTimer)
	if err != nil {
		cancelExecOnce()
		return nil, false, err
	}

	childByID, pendingChildren, timedOutChildren, err := exec.collectAgentChildResults(wfCtx, batch.childFutures, finalizeTimer)
	if err != nil {
		cancelExecOnce()
		return nil, false, err
	}

	timedOut := timedOutActs || timedOutChildren
	if timedOut {
		cancelExecOnce()
	}

	mergeToolResultMaps(batch.inlineByID, childByID)
	if err := exec.handleTimedOutBatch(wfCtx, ctx, timedOut, activityByID, batch.inlineByID, pendingActs, pendingChildren); err != nil {
		return nil, false, err
	}

	merged, err := mergeToolResultsInCallOrder(batch.calls, activityByID, batch.inlineByID)
	if err != nil {
		return nil, false, err
	}
	return merged, timedOut, nil
}

func (e *toolBatchExec) timeBudgetExpired(wfCtx engine.WorkflowContext) bool {
	return !e.finishBy.IsZero() && !wfCtx.Now().Before(e.finishBy)
}

func (e *toolBatchExec) toolQueue(name tools.Ident) string {
	spec, ok := e.r.toolSpec(name)
	if !ok {
		return ""
	}
	e.r.mu.RLock()
	ts, hasTS := e.r.toolsets[spec.Toolset]
	e.r.mu.RUnlock()
	if hasTS && ts.TaskQueue != "" {
		return ts.TaskQueue
	}
	return ""
}

func (e *toolBatchExec) newFinalizeTimer(ctx context.Context, wfCtx engine.WorkflowContext) (engine.Future[time.Time], error) {
	if e.finishBy.IsZero() {
		return nil, nil
	}
	d := e.finishBy.Sub(wfCtx.Now())
	t, err := wfCtx.NewTimer(ctx, d)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func mergeToolResultMaps(dst map[string]*planner.ToolResult, src map[string]*planner.ToolResult) {
	for id, tr := range src {
		dst[id] = tr
	}
}
