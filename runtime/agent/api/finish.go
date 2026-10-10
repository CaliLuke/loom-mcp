package api

import (
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/telemetry"
)

// ToolEventSummary preserves child-run aggregation in a fixed number of fields.
type ToolEventSummary struct {
	// FailedCount counts events carrying tool errors.
	FailedCount int
	// LastError is the last tool error, used when every child tool failed.
	LastError *planner.ToolError
	// Telemetry sums tokens and duration and retains the first reported model.
	Telemetry *telemetry.ToolTelemetry
}

// FinishRunInput is the terminal activity envelope. Referenced Result and
// ServerData bytes are omitted; unreferenced content stays inline.
type FinishRunInput struct {
	// Final is the final assistant message, stored before the run completes.
	Final *model.Message
	// RootRunID is the shared storage scope for this run tree.
	RootRunID string
	// ToolEvents is the ordered list with referenced bytes omitted.
	ToolEvents []*ToolEvent
	// FinalToolResult is the selected completion or planner-owned result.
	FinalToolResult *ToolEvent
}

// FinishRunOutput contains the stored event-list address and bounded final output.
type FinishRunOutput struct {
	// FinalRef addresses Final in the root run scope, when present.
	FinalRef runstore.Hash
	// ToolEventsRef addresses the stored omitted-bytes event list.
	ToolEventsRef runstore.Hash
	// ToolEventCount counts entries in the stored list.
	ToolEventCount int
	// ToolSummary preserves parent aggregation without loading the list.
	ToolSummary ToolEventSummary
	// FinalToolResult is the selected completion or planner-owned result.
	FinalToolResult *ToolEvent
}
