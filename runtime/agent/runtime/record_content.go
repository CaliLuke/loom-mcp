package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/contextmeasure"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// RecordContentActivityName returns the content activity registered on an agent's queue.
func RecordContentActivityName(id agent.Ident) string {
	return string(id) + ".record_content"
}

// RecordContentActivity stores Items in the root scope, returning ordered references
// only after every Put succeeds. Workflows retain references instead of content.
func (r *Runtime) RecordContentActivity(ctx context.Context, input *api.RecordContentInput) (*api.RecordContentOutput, error) {
	if input == nil || input.RootRunID == "" {
		return nil, fmt.Errorf("record content: root run id is required")
	}
	if r.RunStore == nil {
		return nil, fmt.Errorf("record content: run store is required")
	}
	if input.ContextStart != nil {
		return r.recordContextStart(ctx, input)
	}
	if input.FinalizeCalls != nil {
		return r.recordFinalizationCalls(ctx, input)
	}
	if input.Typed != nil {
		data, err := r.RunStore.Get(ctx, input.RootRunID, input.Typed.PayloadRef)
		if err != nil {
			return nil, err
		}
		if err := validateTypedInputPayload(input.Typed.Schema, rawjson.Message(data)); err != nil {
			return nil, err
		}
	}
	if input.Provided != nil {
		return r.recordProvidedContent(ctx, input)
	}
	return r.recordContentItems(ctx, input)
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

// recordWorkflowContent collects content without scheduling an activity. The
// owner flushes the complete step before adopting references or index entries.
func recordWorkflowContent(ctx context.Context, items [][]byte) ([]runstore.Hash, error) {
	batch := contentBatchFromContext(ctx)
	if batch == nil {
		return nil, fmt.Errorf("record content requires a workflow content batch")
	}
	return batch.add(items), nil
}

func seedWorkflowIndex(wf engine.WorkflowContext, input *RunInput) (*api.RecordContentOutput, error) {
	if len(input.Messages) == 0 {
		return &api.RecordContentOutput{}, nil
	}
	items, err := encodeContentMessages(input.Messages)
	if err != nil {
		return nil, err
	}
	return wf.ExecuteRecordContentActivity(wf.Context(), engine.RecordContentActivityCall{
		Name:  RecordContentActivityName(input.AgentID),
		Input: &api.RecordContentInput{RootRunID: input.RootRunID, Items: items, SeedMessages: true},
	})
}

// recordContentItems commits seed segments only after their message content.
func (r *Runtime) recordContentItems(ctx context.Context, input *api.RecordContentInput) (*api.RecordContentOutput, error) {
	refs := make([]runstore.Hash, len(input.Items))
	for i, data := range input.Items {
		hash, err := r.RunStore.Put(ctx, input.RootRunID, data)
		if err != nil {
			return nil, fmt.Errorf("record content item %d: %w", i, err)
		}
		refs[i] = hash
	}
	out := &api.RecordContentOutput{Refs: refs}
	if input.SeedMessages {
		entries := make([]index.MessageEntry, len(refs))
		for i, ref := range refs {
			var message *model.Message
			if err := json.Unmarshal(input.Items[i], &message); err != nil {
				return nil, fmt.Errorf("seed message %d: %w", i, err)
			}
			size, err := contextmeasure.MeasureMessages([]*model.Message{message})
			if err != nil {
				return nil, err
			}
			entries[i] = index.MessageEntry{ID: index.MessageID(i + 1), Kind: index.Message, Hash: ref, Bytes: size.Bytes, NonText: size.NonText, Role: size.Role}
		}
		head, committed, err := storeIndexSegments(ctx, r.RunStore, input.RootRunID, "", "", entries)
		if err != nil {
			return nil, err
		}
		out.IndexHead, out.Entries = head, committed
	}
	return out, nil
}

// recordWorkflowToolResult collects content-bearing results and prepares their
// hashes. The step owner must flush the batch before adopting these references.
func (r *Runtime) recordWorkflowToolResult(ctx context.Context, result *planner.ToolResult, resultJSON []byte) error {
	if result.Error != nil && result.Result == nil {
		if len(result.ServerData) > 0 && result.Refs.ServerData == "" {
			refs, err := recordWorkflowContent(ctx, [][]byte{result.ServerData})
			if err != nil {
				return err
			}
			result.Refs.ServerData = refs[0]
		}
		return nil
	}
	if len(resultJSON) == 0 {
		encoded, err := r.marshalToolValue(ctx, result.Name, result.Result, result.Bounds)
		if err != nil {
			return err
		}
		resultJSON = encoded
	}
	result.Preview = r.formatResultPreview(ctx, result.Name, nil, result.Result, result.Bounds)
	if len(resultJSON) == 0 {
		resultJSON = []byte("null")
	}
	result.ResultBytes = len(resultJSON)
	items := [][]byte{resultJSON}
	if len(result.ServerData) > 0 {
		items = append(items, result.ServerData)
	}
	refs, err := recordWorkflowContent(ctx, items)
	if err != nil {
		return err
	}
	result.Refs = tools.ResultRefs{Result: refs[0]}
	if len(refs) > 1 {
		result.Refs.ServerData = refs[1]
	}
	return nil
}
