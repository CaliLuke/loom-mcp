package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/CaliLuke/loom-mcp/runtime/agent"
	"github.com/CaliLuke/loom-mcp/runtime/agent/tools"
)

// marshalToolValue encodes a tool result using the registered result codec and,
// for bounded tools, projects canonical bounds metadata into the public JSON
// contract emitted by the runtime.
func (r *Runtime) marshalToolValue(ctx context.Context, toolName tools.Ident, value any, bounds *agent.Bounds) (json.RawMessage, error) {
	if value == nil {
		return nil, nil
	}
	spec, ok := r.toolSpec(toolName)
	if !ok {
		r.logger.Error(ctx, "no codec found for tool", "tool", toolName, "payload", false)
		return nil, fmt.Errorf("no codec found for tool %s", toolName)
	}
	projected, err := EncodeCanonicalToolResult(spec, value, bounds)
	if err != nil {
		r.logger.Warn(ctx, "tool result encode failed", "tool", toolName, "payload", false, "err", err)
		return nil, err
	}
	return json.RawMessage(projected), nil
}

// unmarshalToolValue decodes a tool value using the registered codec or standard JSON.
func (r *Runtime) unmarshalToolValue(ctx context.Context, toolName tools.Ident, raw json.RawMessage, payload bool) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	codec, ok := r.toolCodec(toolName, payload)
	if ok && codec.FromJSON != nil {
		v, err := codec.FromJSON(raw)
		if err != nil {
			// Decode failures indicate a contract mismatch between the generated
			// codecs and the concrete payload/result JSON. Log a warning so
			// callers that fall back to raw JSON (e.g. for observability) still
			// surface a precise error for debugging.
			r.logger.Warn(ctx, "tool codec decode failed", "tool", toolName, "payload", payload, "err", err, "json", string(raw))
			return nil, err
		}
		return v, nil
	}
	r.logger.Error(ctx, "no codec found for tool", "tool", toolName, "payload", payload)
	return nil, fmt.Errorf("no codec found for tool %s", toolName)
}

// toolCodec retrieves the JSON codec for a tool's payload or result.
func (r *Runtime) toolCodec(toolName tools.Ident, payload bool) (*tools.JSONCodec[any], bool) {
	spec, ok := r.toolSpec(toolName)
	if !ok {
		return nil, false
	}
	if payload {
		return &spec.Payload.Codec, true
	}
	return &spec.Result.Codec, true
}
