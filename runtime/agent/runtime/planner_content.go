package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
)

// storePlanActivityOutput publishes no activity output until every content write
// succeeds, including framework-authored recovery corrections.
func (r *Runtime) storePlanActivityOutput(ctx context.Context, root string, out *PlanActivityOutput) (*PlanActivityOutput, error) {
	var items [][]byte
	if len(out.Transcript) > 0 {
		data, err := json.Marshal(out.Transcript)
		if err != nil {
			return nil, fmt.Errorf("encode planner transcript: %w", err)
		}
		items = append(items, data)
	}
	if out.Result != nil {
		for _, call := range out.Result.ToolCalls {
			data, err := json.Marshal(call)
			if err != nil {
				return nil, fmt.Errorf("encode planner tool call: %w", err)
			}
			items = append(items, data)
		}
	}
	if out.Recovery != nil && out.Recovery.Correction != "" {
		items = append(items, []byte(out.Recovery.Correction))
	}
	if len(items) == 0 {
		return out, nil
	}
	stored, err := r.RecordContentActivity(ctx, &api.RecordContentInput{RootRunID: root, Items: items})
	if err != nil {
		return nil, err
	}
	refs := stored.Refs
	if len(out.Transcript) > 0 {
		out.TranscriptRef = refs[0]
		refs = refs[1:]
	}
	if out.Result != nil && len(out.Result.ToolCalls) > 0 {
		n := len(out.Result.ToolCalls)
		out.ToolCallRefs = refs[:n:n]
		refs = refs[n:]
	}
	if out.Recovery != nil && out.Recovery.Correction != "" {
		out.Recovery.CorrectionRef = refs[0]
	}
	return out, nil
}
