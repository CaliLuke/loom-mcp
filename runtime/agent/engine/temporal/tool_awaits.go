package temporal

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
)

// QueryToolAwaits resolves pending tool handshakes from the workflow worker.
func (e *Engine) QueryToolAwaits(ctx context.Context, workflowID string) ([]planner.AwaitExternalTools, error) {
	value, err := e.QueryWorkflow(ctx, workflowID, engine.QueryToolAwaits)
	if err != nil {
		return nil, err
	}
	var awaits []planner.AwaitExternalTools
	if err := value.Get(&awaits); err != nil {
		return nil, err
	}
	return awaits, nil
}
