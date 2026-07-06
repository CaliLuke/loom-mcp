package runtime

// tool_result_materialization.go owns the runtime's typed tool-result
// enrichment path.
//
// Contract:
// - All successful tool results, whether executed directly or provided
//   externally through an await signal, are materialized before canonical JSON
//   encoding and hook publication.
// - Toolset-owned server-only sidecars must be attached here so streamed
//   `tool_result` events, durable run logs, and resume inputs all observe the
//   same result shape.
// - External callers provide raw result JSON only; they never construct the
//   runtime's internal `api.ToolEvent` envelope.

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/runtime/agent/artifact"
	"github.com/CaliLuke/loom-mcp/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

// materializeToolResult runs the registered typed result materializer, enforces
// the tool contract, and returns canonical JSON for the final runtime-owned
// tool result payload.
func (r *Runtime) materializeToolResult(ctx context.Context, call planner.ToolRequest, result *planner.ToolResult) (rawjson.Message, error) {
	spec, ok := r.toolSpec(call.Name)
	if !ok {
		return nil, fmt.Errorf("tool %q is not registered", call.Name)
	}
	if err := r.applyResultMaterializer(ctx, spec, call, result); err != nil {
		return nil, err
	}
	if err := r.enforceToolResultContracts(spec, call, result); err != nil {
		return nil, err
	}
	if err := r.persistToolResultArtifacts(ctx, call, result); err != nil {
		return nil, err
	}
	var resultJSON rawjson.Message
	if result.Error == nil {
		encoded, err := r.marshalToolValue(ctx, call.Name, result.Result, result.Bounds)
		if err != nil {
			return nil, fmt.Errorf("encode %s tool result: %w", call.Name, err)
		}
		resultJSON = rawjson.Message(encoded)
	}
	return resultJSON, nil
}

func (r *Runtime) persistToolResultArtifacts(ctx context.Context, call planner.ToolRequest, result *planner.ToolResult) error {
	if result == nil || len(result.Artifacts) == 0 {
		return nil
	}
	for i := range result.Artifacts {
		content := &result.Artifacts[i]
		if err := validateArtifactRefScope(call, content.Ref); err != nil {
			return err
		}
		content.Ref = normalizeArtifactRef(call, content.Ref)
		if content.Ref.ID != "" {
			continue
		}
		if r.ArtifactStore == nil {
			return fmt.Errorf("persist artifacts for %s: artifact store is not configured", call.Name)
		}
		ref, err := r.ArtifactStore.Save(ctx, artifactSaveInput(call, *content))
		if err != nil {
			return fmt.Errorf("persist artifact for %s: %w", call.Name, err)
		}
		content.Ref = ref
		content.SizeBytes = ref.SizeBytes
	}
	return nil
}

func validateArtifactRefScope(call planner.ToolRequest, ref artifact.Ref) error {
	if ref.AgentID != "" && ref.AgentID != string(call.AgentID) {
		return fmt.Errorf("artifact ref scope mismatch: agent_id %q does not match call agent_id %q", ref.AgentID, call.AgentID)
	}
	if ref.RunID != "" && ref.RunID != call.RunID {
		return fmt.Errorf("artifact ref scope mismatch: run_id %q does not match call run_id %q", ref.RunID, call.RunID)
	}
	if ref.ToolCallID != "" && ref.ToolCallID != call.ToolCallID {
		return fmt.Errorf("artifact ref scope mismatch: tool_call_id %q does not match call tool_call_id %q", ref.ToolCallID, call.ToolCallID)
	}
	return nil
}

func normalizeArtifactRef(call planner.ToolRequest, ref artifact.Ref) artifact.Ref {
	if ref.AgentID == "" {
		ref.AgentID = string(call.AgentID)
	}
	if ref.RunID == "" {
		ref.RunID = call.RunID
	}
	if ref.ToolCallID == "" {
		ref.ToolCallID = call.ToolCallID
	}
	return ref
}

func artifactSaveInput(call planner.ToolRequest, content artifact.Content) artifact.SaveInput {
	ref := normalizeArtifactRef(call, content.Ref)
	return artifact.SaveInput{
		AgentID:    ref.AgentID,
		RunID:      ref.RunID,
		ToolCallID: ref.ToolCallID,
		Name:       ref.Name,
		MimeType:   ref.MimeType,
		Metadata:   ref.Metadata,
		Body:       content.Body,
	}
}

// materializeToolExecutionResult validates the runtime-owned execution wrapper,
// materializes the durable tool result, and returns the current-batch pause
// signal separately from planner-visible history.
func (r *Runtime) materializeToolExecutionResult(
	ctx context.Context,
	call planner.ToolRequest,
	exec *ToolExecutionResult,
) (*planner.ToolResult, rawjson.Message, *ToolPause, error) {
	if exec == nil {
		return nil, nil, nil, fmt.Errorf("tool %q returned nil execution result", call.Name)
	}
	if exec.ToolResult == nil {
		return nil, nil, nil, fmt.Errorf("tool %q returned nil tool result", call.Name)
	}
	resultJSON, err := r.materializeToolResult(ctx, call, exec.ToolResult)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := validateToolPauseContract(call, exec.ToolResult, exec.Pause); err != nil {
		return nil, nil, nil, err
	}
	return exec.ToolResult, resultJSON, exec.Pause, nil
}

// applyResultMaterializer invokes the toolset-owned typed result materializer
// when the toolset registered one.
func (r *Runtime) applyResultMaterializer(ctx context.Context, spec tools.ToolSpec, call planner.ToolRequest, result *planner.ToolResult) error {
	if result == nil {
		return fmt.Errorf("nil tool result for %q (%s)", call.Name, call.ToolCallID)
	}
	if result.Name == "" {
		result.Name = call.Name
	}
	r.mu.RLock()
	reg, ok := r.toolsets[spec.Toolset]
	r.mu.RUnlock()
	if !ok || reg.ResultMaterializer == nil {
		return nil
	}
	if err := reg.ResultMaterializer(ctx, toolCallMeta(call), &call, result); err != nil {
		return fmt.Errorf("materialize %s tool result: %w", call.Name, err)
	}
	return nil
}

// decodeProvidedToolResults decodes externally supplied raw tool results in the
// canonical awaited call order and materializes their runtime-owned sidecars.
func (r *Runtime) decodeProvidedToolResults(ctx context.Context, allowed []planner.ToolRequest, provided map[string]*api.ProvidedToolResult) ([]*planner.ToolResult, []rawjson.Message, error) {
	if len(allowed) == 0 {
		return nil, nil, nil
	}
	results := make([]*planner.ToolResult, 0, len(allowed))
	resultJSONs := make([]rawjson.Message, 0, len(allowed))
	for _, call := range allowed {
		item := provided[call.ToolCallID]
		if item == nil {
			return nil, nil, fmt.Errorf("await: missing tool result for tool_call_id %q", call.ToolCallID)
		}
		if item.Name != call.Name {
			return nil, nil, fmt.Errorf("await: result tool %q does not match awaited tool %q", item.Name, call.Name)
		}
		spec, ok := r.toolSpec(call.Name)
		if !ok {
			return nil, nil, fmt.Errorf("await: tool %q is not registered", call.Name)
		}
		result, resultJSON, err := r.decodeProvidedToolResult(ctx, spec, call, item)
		if err != nil {
			return nil, nil, err
		}
		results = append(results, result)
		resultJSONs = append(resultJSONs, resultJSON)
	}
	return results, resultJSONs, nil
}

// decodeProvidedToolResult converts one externally supplied raw result into the
// typed planner result used by the runtime.
func (r *Runtime) decodeProvidedToolResult(ctx context.Context, spec tools.ToolSpec, call planner.ToolRequest, item *api.ProvidedToolResult) (*planner.ToolResult, rawjson.Message, error) {
	if item == nil {
		return nil, nil, fmt.Errorf("await: nil tool result")
	}
	resultProvided := hasNonNullJSON(item.Result.RawMessage())
	if item.Error != nil && resultProvided {
		return nil, nil, fmt.Errorf("await: tool result for %s is invalid: error and result are both set", call.Name)
	}
	bounds := cloneProvidedToolBounds(item.Bounds)
	var decoded any
	var err error
	if resultProvided && item.Error == nil {
		decoded, err = spec.Result.Codec.FromJSON(item.Result.RawMessage())
		if err != nil {
			return nil, nil, fmt.Errorf("await: decode tool result for %s: %w", call.Name, err)
		}
	}
	result := &planner.ToolResult{
		Name:       call.Name,
		Result:     decoded,
		ServerData: nil,
		Bounds:     bounds,
		Error:      item.Error,
		RetryHint:  item.RetryHint,
		ToolCallID: call.ToolCallID,
		Artifacts:  artifactContentsFromRefs(item.Artifacts),
	}
	resultJSON, err := r.materializeToolResult(ctx, call, result)
	if err != nil {
		return nil, nil, fmt.Errorf("await: %w", err)
	}
	return result, resultJSON, nil
}

// cloneProvidedToolBounds copies provided bounds metadata into an internal
// planner result. Contract validation is centralized in materializeToolResult.
func cloneProvidedToolBounds(bounds *agent.Bounds) *agent.Bounds {
	if bounds == nil {
		return nil
	}
	c := *bounds
	if bounds.Total != nil {
		total := *bounds.Total
		c.Total = &total
	}
	if bounds.NextCursor != nil {
		next := *bounds.NextCursor
		c.NextCursor = &next
	}
	return &c
}

func toolCallMeta(call planner.ToolRequest) ToolCallMeta {
	return ToolCallMeta{
		RunID:            call.RunID,
		SessionID:        call.SessionID,
		TurnID:           call.TurnID,
		ToolCallID:       call.ToolCallID,
		ParentToolCallID: call.ParentToolCallID,
	}
}
