package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/run"
)

// attachRunLink stamps the parent tool result with a run handle linking to the nested agent run that produced it.
func attachRunLink(result *planner.ToolResult, handle *run.Handle) {
	result.RunLink = handle
}

// adaptAgentChildOutput converts a nested agent RunOutput into a planner.ToolResult.
func (r *Runtime) adaptAgentChildOutput(ctx context.Context, cfg *AgentToolConfig, call *planner.ToolRequest, nestedRunCtx run.Context, outPtr *RunOutput) (*planner.ToolResult, error) {
	if outPtr == nil {
		return nil, fmt.Errorf("execute agent returned no output")
	}

	handle := &run.Handle{
		RunID:            nestedRunCtx.RunID,
		AgentID:          cfg.AgentID,
		ParentRunID:      nestedRunCtx.ParentRunID,
		ParentToolCallID: nestedRunCtx.ParentToolCallID,
	}

	if outPtr.FinalToolResult != nil {
		tr, err := r.decodeAgentChildFinalToolResult(ctx, call, outPtr.FinalToolResult)
		if err != nil {
			return nil, err
		}
		tr.ToolCallID = call.ToolCallID
		tr.ChildrenCount = len(outPtr.ToolEvents)
		attachRunLink(tr, handle)
		return tr, nil
	}

	result := ConvertRunOutputToToolResult(call.Name, outPtr)
	result.ToolCallID = call.ToolCallID
	attachRunLink(&result, handle)
	tr := &result
	return tr, nil
}

// decodeAgentChildFinalToolResult decodes the workflow-safe final tool-result envelope emitted by a nested child run.
func (r *Runtime) decodeAgentChildFinalToolResult(ctx context.Context, call *planner.ToolRequest, event *api.ToolEvent) (*planner.ToolResult, error) {
	if call == nil {
		return nil, errors.New("agent-tool final result: tool call is required")
	}
	if event == nil {
		return nil, fmt.Errorf("agent-tool final result for %s: event is nil", call.Name)
	}
	result := &planner.ToolResult{
		Name:                call.Name,
		ResultBytes:         event.ResultBytes,
		ResultOmitted:       event.ResultOmitted,
		ResultOmittedReason: event.ResultOmittedReason,
		ServerData:          append(rawjson.Message(nil), event.ServerData...),
		Bounds:              event.Bounds,
		Error:               event.Error,
		RetryHint:           event.RetryHint,
		Telemetry:           event.Telemetry,
	}
	if hasNonNullJSON(event.Result.RawMessage()) && event.Error == nil {
		decoded, err := r.unmarshalToolValue(ctx, call.Name, event.Result.RawMessage(), false)
		if err != nil {
			return nil, fmt.Errorf("decode final tool result for %s: %w", call.Name, err)
		}
		result.Result = decoded
	}
	return result, nil
}
