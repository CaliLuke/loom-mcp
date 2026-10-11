package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// storePlanActivityOutput publishes no activity output until every content write
// succeeds, including framework-authored recovery corrections.
func (r *Runtime) storePlanActivityOutput(ctx context.Context, root string, out *PlanActivityOutput, transcript []*model.Message, awaitCalls []planner.ToolRequest) (*PlanActivityOutput, error) {
	size, err := contextmeasure.MeasureMessages(transcript)
	if err != nil {
		return nil, err
	}
	out.TranscriptSize = size
	items, err := encodePlanActivityContent(out, transcript, awaitCalls)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return out, nil
	}
	stored, err := r.RecordContentActivity(ctx, &api.RecordContentInput{RootRunID: root, Items: items})
	if err != nil {
		return nil, err
	}
	refs := stored.Refs
	if len(transcript) > 0 {
		out.TranscriptRef = refs[0]
		refs = refs[1:]
	}
	if out.Result != nil && len(out.Result.ToolCalls) > 0 {
		n := len(out.Result.ToolCalls)
		out.ToolCallRefs = refs[:n:n]
		refs = refs[n:]
	}
	if len(awaitCalls) > 0 {
		out.AwaitCallRefs = make(map[string]runstore.Hash, len(awaitCalls))
		for i, call := range awaitCalls {
			out.AwaitCallRefs[call.ToolCallID] = refs[i]
		}
		refs = refs[len(awaitCalls):]
	}
	if out.Recovery != nil && out.Recovery.Correction != "" {
		out.Recovery.CorrectionRef = refs[0]
		out.Recovery.Correction = ""
	}
	return out, nil
}

// Await calls carry only the fields published and dispatched by the await path.
func canonicalAwaitCalls(result *planner.PlanResult) []planner.ToolRequest {
	if result == nil || result.Await == nil {
		return nil
	}
	var calls []planner.ToolRequest
	for _, item := range result.Await.Items {
		if q := item.Questions; q != nil {
			calls = append(calls, planner.ToolRequest{Name: q.ToolName, ToolCallID: q.ToolCallID, Payload: q.Payload})
		}
		if e := item.ExternalTools; e != nil {
			for _, item := range e.Items {
				calls = append(calls, planner.ToolRequest{Name: item.Name, ToolCallID: item.ToolCallID, Payload: item.Payload})
			}
		}
	}
	return calls
}

func validateRecoveryCorrection(text string) error {
	if strings.TrimSpace(text) == "" || len(text) > maxRecoveryCorrectionBytes || !utf8.ValidString(text) {
		return fmt.Errorf("correction must be non-empty, valid UTF-8, and within the recovery bound")
	}
	return nil
}

// encodePlanActivityContent fixes the transcript, dispatched-call, and correction
// ordering used when assigning the corresponding content references.
func encodePlanActivityContent(out *PlanActivityOutput, transcript []*model.Message, awaitCalls []planner.ToolRequest) ([][]byte, error) {
	var items [][]byte
	if len(transcript) > 0 {
		data, err := json.Marshal(transcript, json.Deterministic(true))
		if err != nil {
			return nil, fmt.Errorf("encode planner transcript: %w", err)
		}
		items = append(items, data)
	}
	if out.Result != nil {
		for _, call := range out.Result.ToolCalls {
			data, err := json.Marshal(call, json.Deterministic(true))
			if err != nil {
				return nil, fmt.Errorf("encode planner tool call: %w", err)
			}
			items = append(items, data)
		}
	}
	for _, call := range awaitCalls {
		data, err := json.Marshal(call, json.Deterministic(true))
		if err != nil {
			return nil, err
		}
		items = append(items, data)
	}
	if out.Recovery != nil && out.Recovery.Correction != "" {
		if err := validateRecoveryCorrection(out.Recovery.Correction); err != nil {
			return nil, err
		}
		items = append(items, []byte(out.Recovery.Correction))
	}
	return items, nil
}
