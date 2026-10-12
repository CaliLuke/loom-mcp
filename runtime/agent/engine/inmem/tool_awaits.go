package inmem

import (
	"context"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
)

// QueryToolAwaits returns a snapshot of the workflow's pending tool handshakes.
func (e *eng) QueryToolAwaits(ctx context.Context, workflowID string) ([]planner.AwaitExternalTools, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if workflowID == "" {
		return nil, errors.New("workflow id is required")
	}
	e.mu.RLock()
	h, ok := e.handles[workflowID]
	if !ok {
		e.mu.RUnlock()
		return nil, engine.ErrWorkflowNotFound
	}
	handler := h.wfCtx.queries[engine.QueryToolAwaits]
	e.mu.RUnlock()
	query, ok := handler.(func() ([]planner.AwaitExternalTools, error))
	if !ok {
		return nil, fmt.Errorf("workflow %q: tool_awaits query not registered", workflowID)
	}
	return query()
}
