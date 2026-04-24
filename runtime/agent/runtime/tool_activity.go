package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

// ExecuteToolActivity runs a tool invocation as a workflow activity.
//
// Advanced & generated integration
//   - Intended to be registered by generated code with the workflow engine.
//   - Normal applications should use AgentClient (Runtime.Client(...).Run/Start)
//     rather than invoking activities directly.
//
// It decodes the tool payload, runs the registered tool implementation, and
// encodes the result using the tool‑specific codec. Returns an error if the
// toolset is not registered or if encoding/decoding fails.
func (r *Runtime) ExecuteToolActivity(ctx context.Context, req *ToolInput) (*ToolOutput, error) {
	stopHeartbeat := startActivityHeartbeat(ctx)
	defer stopHeartbeat()

	reg, raw, meta, out, err := r.prepareToolActivity(ctx, req)
	if err != nil {
		return nil, err
	}
	if out != nil {
		return out, nil
	}
	call := planner.ToolRequest{
		Name:             req.ToolName,
		Payload:          raw,
		RunID:            req.RunID,
		AgentID:          req.AgentID,
		SessionID:        req.SessionID,
		TurnID:           req.TurnID,
		ParentToolCallID: req.ParentToolCallID,
		ToolCallID:       req.ToolCallID,
	}
	start := time.Now()
	result, err := reg.Execute(ctx, &call)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("tool execution returned nil result")
	}
	applyToolActivityTelemetry(ctx, reg, meta, req.ToolName, start, result)
	resultJSON, err := r.materializeToolResult(ctx, call, result)
	if err != nil {
		return nil, err
	}
	return newToolActivityOutput(resultJSON, result), nil
}

func applyToolActivityTelemetry(
	ctx context.Context,
	reg ToolsetRegistration,
	meta ToolCallMeta,
	toolName tools.Ident,
	start time.Time,
	result *planner.ToolResult,
) {
	if reg.TelemetryBuilder == nil || result.Telemetry != nil {
		return
	}
	if tel := reg.TelemetryBuilder(ctx, meta, toolName, start, time.Now(), nil); tel != nil {
		result.Telemetry = tel
	}
}

func newToolActivityOutput(resultJSON rawjson.Message, result *planner.ToolResult) *ToolOutput {
	resultOut := &ToolOutput{
		Payload:    resultJSON,
		Bounds:     result.Bounds,
		ServerData: result.ServerData,
		Telemetry:  result.Telemetry,
	}
	if result.Error != nil {
		resultOut.Error = result.Error.Error()
	}
	if result.RetryHint != nil {
		resultOut.RetryHint = result.RetryHint
	}
	return resultOut
}

func (r *Runtime) prepareToolActivity(ctx context.Context, req *ToolInput) (ToolsetRegistration, rawjson.Message, ToolCallMeta, *ToolOutput, error) {
	if err := r.validateToolActivityRequest(req); err != nil {
		return ToolsetRegistration{}, nil, ToolCallMeta{}, nil, err
	}
	reg, err := r.resolveToolsetRegistration(req)
	if err != nil {
		return ToolsetRegistration{}, nil, ToolCallMeta{}, nil, err
	}
	meta := toolCallMeta(planner.ToolRequest{
		RunID:            req.RunID,
		SessionID:        req.SessionID,
		TurnID:           req.TurnID,
		ToolCallID:       req.ToolCallID,
		ParentToolCallID: req.ParentToolCallID,
	})
	raw, out := r.adaptAndValidateToolPayload(ctx, reg, req, meta)
	return reg, raw, meta, out, nil
}

func (r *Runtime) validateToolActivityRequest(req *ToolInput) error {
	if req == nil {
		return errors.New("tool input is required")
	}
	if req.ToolName == "" {
		return errors.New("tool name is required")
	}
	if spec, ok := r.toolSpec(req.ToolName); ok && spec.IsAgentTool {
		if string(req.AgentID) == spec.AgentID {
			return fmt.Errorf(
				"agent %q attempted to execute its own agent-as-tool %q via ExecuteToolActivity; "+
					"agent-as-tools must run inline in workflow context and must not be exposed to the provider's planner tool list",
				req.AgentID,
				req.ToolName,
			)
		}
		return fmt.Errorf("agent-as-tool %q must run in workflow context", req.ToolName)
	}
	return nil
}

func (r *Runtime) resolveToolsetRegistration(req *ToolInput) (ToolsetRegistration, error) {
	sName := req.ToolsetName
	if sName == "" {
		spec, ok := r.toolSpec(req.ToolName)
		if !ok {
			return ToolsetRegistration{}, fmt.Errorf("unknown tool %q", req.ToolName)
		}
		sName = spec.Toolset
	}
	r.mu.RLock()
	reg, ok := r.toolsets[sName]
	r.mu.RUnlock()
	if !ok {
		return ToolsetRegistration{}, fmt.Errorf("toolset %q is not registered", sName)
	}
	return reg, nil
}

func (r *Runtime) adaptAndValidateToolPayload(
	ctx context.Context,
	reg ToolsetRegistration,
	req *ToolInput,
	meta ToolCallMeta,
) (rawjson.Message, *ToolOutput) {
	raw := req.Payload
	if reg.PayloadAdapter != nil && len(raw) > 0 {
		adapted, err := reg.PayloadAdapter(ctx, meta, req.ToolName, raw.RawMessage())
		if err != nil {
			return nil, &ToolOutput{Error: fmt.Sprintf("payload adapter failed: %v", err)}
		}
		if len(adapted) > 0 {
			raw = rawjson.Message(adapted)
		}
	}
	if reg.DecodeInExecutor || len(raw) == 0 {
		return raw, nil
	}
	if _, decErr := r.unmarshalToolValue(ctx, req.ToolName, raw.RawMessage(), true); decErr != nil {
		return nil, r.toolDecodeErrorOutput(req.ToolName, decErr)
	}
	return raw, nil
}
