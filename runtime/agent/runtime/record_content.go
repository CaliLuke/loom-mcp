package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

// RecordContentActivityName returns the content activity registered on an agent's queue.
func RecordContentActivityName(id agent.Ident) string {
	return string(id) + ".record_content"
}

// RecordContentActivity stores Items in the root scope, returning ordered references
// only after every Put succeeds. Inline bytes remain in their original envelopes.
func (r *Runtime) RecordContentActivity(ctx context.Context, input *api.RecordContentInput) (*api.RecordContentOutput, error) {
	if input == nil || input.RootRunID == "" {
		return nil, fmt.Errorf("record content: root run id is required")
	}
	if r.RunStore == nil {
		return nil, fmt.Errorf("record content: run store is required")
	}
	refs := make([]runstore.Hash, len(input.Items))
	for i, data := range input.Items {
		hash, err := r.RunStore.Put(ctx, input.RootRunID, data)
		if err != nil {
			return nil, fmt.Errorf("record content item %d: %w", i, err)
		}
		refs[i] = hash
	}
	return &api.RecordContentOutput{Refs: refs}, nil
}

func encodeContentMessages(messages []*model.Message) ([][]byte, error) {
	items := make([][]byte, len(messages))
	for i, message := range messages {
		data, err := json.Marshal(message)
		if err != nil {
			return nil, fmt.Errorf("encode content message %d: %w", i, err)
		}
		items[i] = data
	}
	return items, nil
}

// recordWorkflowMessages encodes messages individually and preserves their order.
func recordWorkflowMessages(ctx context.Context, id agent.Ident, root string, messages []*model.Message) ([]runstore.Hash, error) {
	items, err := encodeContentMessages(messages)
	if err != nil {
		return nil, err
	}
	return recordWorkflowContent(ctx, id, root, items)
}

// recordWorkflowContent performs no store access in workflow code.
func recordWorkflowContent(ctx context.Context, agentID agent.Ident, root string, items [][]byte) ([]runstore.Hash, error) {
	if len(items) == 0 {
		return nil, nil
	}
	wf := engine.WorkflowContextFromContext(ctx)
	if wf == nil {
		return nil, fmt.Errorf("record content: workflow context is required")
	}
	out, err := wf.ExecuteRecordContentActivity(ctx, engine.RecordContentActivityCall{
		Name: RecordContentActivityName(agentID), Input: &api.RecordContentInput{RootRunID: root, Items: items},
	})
	if err != nil {
		return nil, err
	}
	if out == nil || len(out.Refs) != len(items) {
		return nil, fmt.Errorf("record content: activity returned invalid reference count")
	}
	return out.Refs, nil
}

// recordWorkflowToolResult stores only content-bearing results. Error-only and
// structural synthesized results have no eligible content and never call this path.
func (r *Runtime) recordWorkflowToolResult(ctx context.Context, id agent.Ident, root string, result *planner.ToolResult, resultJSON []byte) error {
	if result.Result == nil {
		return nil
	}
	if len(resultJSON) == 0 {
		encoded, err := r.marshalToolValue(ctx, result.Name, result.Result, result.Bounds)
		if err != nil {
			return err
		}
		resultJSON = encoded
	}
	items := [][]byte{resultJSON}
	if len(result.ServerData) > 0 {
		items = append(items, result.ServerData)
	}
	refs, err := recordWorkflowContent(ctx, id, root, items)
	if err != nil {
		return err
	}
	result.Refs = tools.ResultRefs{Result: refs[0]}
	if len(refs) > 1 {
		result.Refs.ServerData = refs[1]
	}
	return nil
}
