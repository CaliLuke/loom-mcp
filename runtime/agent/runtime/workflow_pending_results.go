package runtime

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
)

// flushUnindexedResults makes completed results of aborted indexed batches canonical
// on the detached terminal path. Calls without assistant entries are never eligible.
func (r *Runtime) flushUnindexedResults(ctx context.Context, base *workflowPlanInput) error {
	var calls []planner.ToolRequest
	var results []*planner.ToolResult
	for _, call := range base.unindexedCalls {
		if result := base.unindexedResults[call.ToolCallID]; result != nil {
			calls = append(calls, call)
			results = append(results, result)
		}
	}
	if len(results) == 0 {
		return nil
	}
	return r.appendIndexResults(ctx, base, calls, results, false)
}

// noteIndexedCall enrolls an indexed assistant call for aborted-batch cleanup.
// Payload bytes remain in the stored call; nil marks a result not yet completed.
func (b *workflowPlanInput) noteIndexedCall(call planner.ToolRequest) {
	if b.unindexedResults == nil {
		b.unindexedResults = make(map[string]*planner.ToolResult)
	}
	call.Payload = nil
	b.unindexedCalls = append(b.unindexedCalls, call)
	b.unindexedResults[call.ToolCallID] = nil
}

// stageUnindexedResult retains bounded metadata for indexed assistant calls only.
// Finalization calls are execution-only and must not add orphan transcript results.
func (b *workflowPlanInput) stageUnindexedResult(result *planner.ToolResult) {
	if _, indexed := b.unindexedResults[result.ToolCallID]; !indexed {
		return
	}
	copy := *result
	copy.Result, copy.ServerData = nil, nil
	b.unindexedResults[result.ToolCallID] = &copy
}

func (b *workflowPlanInput) consumeIndexedResults(results []*planner.ToolResult) {
	for _, result := range results {
		delete(b.unindexedResults, result.ToolCallID)
	}
	if len(b.unindexedResults) == 0 {
		b.unindexedCalls, b.unindexedResults = nil, nil
	}
}
