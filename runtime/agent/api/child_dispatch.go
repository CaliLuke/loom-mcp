package api

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/run"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// ChildDispatchInput carries the call and the parent's view, never its messages.
type ChildDispatchInput struct {
	Call      planner.ToolRequest
	ParentRun run.Context
	View      index.ViewRef
}

// ChildDispatchOutput records validation before a child workflow can start.
type ChildDispatchOutput struct {
	// IndexHead commits the supplied Pending entries when the validator loaded them.
	// It is empty if no view was loaded or the supplied Pending was empty.
	IndexHead runstore.Hash
	Error     string
	RetryHint *planner.RetryHint
}
