package engine

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
)

// ToolAwaitQuerier exposes pending tool handshakes to result-submitting clients.
// Engines must implement this interface to support Runtime.ProvideToolResults.
type ToolAwaitQuerier interface {
	QueryToolAwaits(ctx context.Context, workflowID string) ([]planner.AwaitExternalTools, error)
}

// QueryToolAwaits names the read-only pending tool-handshake workflow query.
const QueryToolAwaits = "tool_awaits"
