package runtime

// tool_events.go contains helpers for encoding and decoding tool results into
// workflow-boundary safe envelopes.
//
// Contract:
// - planner.ToolResult contains `any` fields (Result). Crossing a
//   workflow boundary with those values allows engines/codecs (e.g. Temporal) to
//   rehydrate them as map[string]any, breaking tool codecs.
// - encodeToolEvents converts typed tool results into api.ToolEvent values that
//   only contain canonical JSON bytes plus structured metadata.

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// encodeToolEvents converts typed in-memory tool results into workflow-boundary safe
// envelopes.
//
// Contract:
//   - Input values are trusted to be runtime-produced tool results; nil entries are
//     a bug.
//   - Tool result values are encoded via the registered tool result codec and stored
//     as canonical JSON bytes on api.ToolEvent.Result only without Refs.Result.
//   - Referenced Result and ServerData bytes are omitted for the finish activity.
func (r *Runtime) encodeToolEvents(ctx context.Context, events []*planner.ToolResult) ([]*api.ToolEvent, error) {
	if len(events) == 0 {
		return nil, nil
	}
	out := make([]*api.ToolEvent, 0, len(events))
	for _, ev := range events {
		var result []byte
		resultBytes := ev.ResultBytes
		if ev.Refs.Result == "" {
			var err error
			result, err = r.marshalToolValue(ctx, ev.Name, ev.Result, ev.Bounds)
			if err != nil {
				return nil, fmt.Errorf("encode tool result for %s: %w", ev.Name, err)
			}
			resultBytes = len(result)
		}
		out = append(out, &api.ToolEvent{
			Refs:          ev.Refs,
			Preview:       ev.Preview,
			Name:          ev.Name,
			Result:        rawjson.Message(result),
			ResultBytes:   resultBytes,
			ResultOmitted: false,
			ServerData:    inlineContent(ev.ServerData, ev.Refs.ServerData),
			Bounds:        ev.Bounds,
			Error:         ev.Error,
			RetryHint:     ev.RetryHint,
			Telemetry:     ev.Telemetry,
			ToolCallID:    ev.ToolCallID,
			ChildrenCount: ev.ChildrenCount,
			RunLink:       ev.RunLink,
			Artifacts:     artifactRefsFromContents(ev.Artifacts),
		})
	}
	return out, nil
}

// buildPlannerToolOutputs converts executed tool calls plus their results into
// planner ToolOutput values suitable for run-loop state.
//
// Contract:
//   - Calls are emitted in canonical call order, keyed by ToolCallID.
//   - Results may arrive in a different order (for example, externally provided
//     await results); they are matched by ToolCallID, not slice position.
//   - If a result was already omitted at an upstream truthful boundary, the
//     original omission metadata is preserved without re-encoding synthetic bytes.
func (r *Runtime) buildPlannerToolOutputs(ctx context.Context, calls []planner.ToolRequest, results []*planner.ToolResult) ([]*planner.ToolOutput, error) {
	if len(calls) == 0 {
		return nil, nil
	}
	if len(calls) != len(results) {
		return nil, fmt.Errorf("encode tool outputs: calls/results length mismatch (%d != %d)", len(calls), len(results))
	}
	resultsByToolCallID, err := indexToolResultsByCallID(results)
	if err != nil {
		return nil, err
	}
	out := make([]*planner.ToolOutput, 0, len(calls))
	for _, call := range calls {
		output, err := r.buildPlannerToolOutput(ctx, call, resultsByToolCallID)
		if err != nil {
			return nil, err
		}
		out = append(out, output)
	}
	return out, nil
}

func indexToolResultsByCallID(results []*planner.ToolResult) (map[string]*planner.ToolResult, error) {
	resultsByToolCallID := make(map[string]*planner.ToolResult, len(results))
	for _, result := range results {
		if result == nil {
			return nil, fmt.Errorf("encode tool outputs: nil tool result")
		}
		if result.ToolCallID == "" {
			return nil, fmt.Errorf("encode tool outputs: missing result tool_call_id for %s", result.Name)
		}
		if _, exists := resultsByToolCallID[result.ToolCallID]; exists {
			return nil, fmt.Errorf("encode tool outputs: duplicate result tool_call_id %s", result.ToolCallID)
		}
		resultsByToolCallID[result.ToolCallID] = result
	}
	return resultsByToolCallID, nil
}

func (r *Runtime) buildPlannerToolOutput(
	ctx context.Context,
	call planner.ToolRequest,
	resultsByToolCallID map[string]*planner.ToolResult,
) (*planner.ToolOutput, error) {
	if call.ToolCallID == "" {
		return nil, fmt.Errorf("build planner tool outputs: missing call tool_call_id for %s", call.Name)
	}
	result, ok := resultsByToolCallID[call.ToolCallID]
	if !ok {
		return nil, fmt.Errorf("build planner tool outputs: missing result for tool_call_id %s", call.ToolCallID)
	}
	if result.Name != "" && result.Name != call.Name {
		return nil, fmt.Errorf("build planner tool outputs: result name %s does not match call %s", result.Name, call.Name)
	}
	if err := r.persistToolResultArtifacts(ctx, call, result); err != nil {
		return nil, err
	}
	output := &planner.ToolOutput{
		Refs:                result.Refs,
		Preview:             result.Preview,
		Name:                call.Name,
		ToolCallID:          call.ToolCallID,
		Payload:             append(rawjson.Message(nil), call.Payload...),
		ResultBytes:         result.ResultBytes,
		ResultOmitted:       result.ResultOmitted,
		ResultOmittedReason: result.ResultOmittedReason,
		ServerData:          append(rawjson.Message(nil), result.ServerData...),
		Bounds:              result.Bounds,
		Error:               result.Error,
		RetryHint:           result.RetryHint,
		Telemetry:           result.Telemetry,
		Artifacts:           artifactRefsFromContents(result.Artifacts),
	}
	if result.ResultOmitted || result.Refs.Result != "" {
		return output, nil
	}
	resultJSON, err := r.marshalToolValue(ctx, call.Name, result.Result, result.Bounds)
	if err != nil {
		return nil, fmt.Errorf("build planner tool output result for %s: %w", call.Name, err)
	}
	output.Result = rawjson.Message(resultJSON)
	output.ResultBytes = len(resultJSON)
	return output, nil
}

// inlineContent copies only content that has not already been stored.
func inlineContent(data rawjson.Message, ref runstore.Hash) rawjson.Message {
	if ref != "" {
		return nil
	}
	return append(rawjson.Message(nil), data...)
}
