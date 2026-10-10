package runtime

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
)

func (r *Runtime) decodeAgentToolPromptPayload(ctx context.Context, call planner.ToolRequest) (any, error) {
	if len(call.Payload) == 0 {
		return nil, nil
	}
	if _, ok := r.ToolSpec(call.Name); !ok {
		return nil, fmt.Errorf("agent tool %s requires a registered ToolSpec for payload decoding (missing specs/codecs)", call.Name)
	}
	val, err := r.unmarshalToolValue(ctx, call.Name, call.Payload.RawMessage(), true)
	if err != nil {
		return nil, fmt.Errorf("decode agent tool payload for %s: %w", call.Name, err)
	}
	return val, nil
}
