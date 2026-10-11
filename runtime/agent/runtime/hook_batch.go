package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
)

const publishHookBatchCommand = "runtime.publish_hook_batch"

// publishWorkflowHookBatch shares one activity while retaining each event's
// canonical route and identity. The adoption record precedes following events.
func (r *Runtime) publishWorkflowHookBatch(wf engine.WorkflowContext, turnID string, events ...hooks.Event) error {
	inputs := make([]*hooks.ActivityInput, len(events))
	for i, event := range events {
		input, err := hooks.EncodeToHookInput(event, turnID)
		if err != nil {
			return fmt.Errorf("encode batched hook %d: %w", i, err)
		}
		if len(input.Payload) > maxHookPayloadBytes {
			return fmt.Errorf("batched hook %d exceeds payload limit: %d bytes", i, len(input.Payload))
		}
		inputs[i] = input
	}
	if len(inputs) == 0 {
		return fmt.Errorf("hook batch requires events")
	}
	payload, err := json.Marshal(inputs, json.Deterministic(true))
	if err != nil {
		return fmt.Errorf("encode hook batch: %w", err)
	}
	input := *inputs[0]
	input.Type, input.Payload = publishHookBatchCommand, payload
	return wf.PublishHook(wf.Context(), engine.HookActivityCall{Name: hookActivityName, Input: &input})
}

// hookBatchActivity reuses canonical append-before-projection handling for records
// and the ordinary interceptor contract for each following publication.
func (r *Runtime) hookBatchActivity(ctx context.Context, input *HookActivityInput) error {
	var inputs []*hooks.ActivityInput
	if err := json.Unmarshal(input.Payload, &inputs); err != nil {
		return fmt.Errorf("decode hook batch: %w", err)
	}
	if len(inputs) == 0 {
		return fmt.Errorf("hook batch requires events")
	}
	for _, event := range inputs {
		if err := r.hookActivity(ctx, event); err != nil {
			return err
		}
	}
	return nil
}
