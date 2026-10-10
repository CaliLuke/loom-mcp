package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/tools"
)

type (
	toolPayloadRecorder struct {
		payload []byte
	}
	toolPayloadRecorderKey struct{}
)

// RecordToolPayload records the payload immediately before invoking a tool's
// implementation, after mapping and injection. Generated dispatchers call it to
// report the actual server-only input to ExecuteToolActivity. Custom executors
// that transform their input must also call it with the final value.
// It encodes a snapshot as JSON inside the activity; outside an activity it is a
// no-op. Encoding failures must be returned by the executor.
func RecordToolPayload(ctx context.Context, payload any) error {
	recorder, ok := ctx.Value(toolPayloadRecorderKey{}).(*toolPayloadRecorder)
	if !ok {
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode executed tool payload: %w", err)
	}
	recorder.payload = data
	return nil
}

// LoadToolResult loads the full eligible result from the root run scope and
// decodes it with the tool's generated result codec. Missing hashes and content
// fail immediately; this helper never loads server-only content implicitly.
func LoadToolResult(ctx context.Context, store runstore.Store, scope string, spec tools.ToolSpec, hash runstore.Hash) (any, error) {
	if hash == "" {
		return nil, fmt.Errorf("load tool %s result: %w: empty hash", spec.Name, runstore.ErrNotFound)
	}
	if store == nil {
		return nil, fmt.Errorf("load tool %s result: run store is required", spec.Name)
	}
	if spec.Result.Codec.FromJSON == nil {
		return nil, fmt.Errorf("no result codec found for tool %s", spec.Name)
	}
	data, err := store.Get(ctx, scope, hash)
	if err != nil {
		return nil, fmt.Errorf("load tool %s result: %w", spec.Name, err)
	}
	value, err := spec.Result.Codec.FromJSON(data)
	if err != nil {
		return nil, fmt.Errorf("decode stored tool %s result: %w", spec.Name, err)
	}
	return value, nil
}

// recordActivityRequest snapshots the input of executors that consume raw JSON.
// Generated dispatchers replace this snapshot after their typed transformations.
func recordActivityRequest(ctx context.Context, payload rawjson.Message) error {
	if len(payload) == 0 {
		return RecordToolPayload(ctx, nil)
	}
	return RecordToolPayload(ctx, payload)
}

// storedToolActivityOutput returns the inline envelope only after storing all
// referenced content. A failed write cannot publish a partially stored result.
func (r *Runtime) storedToolActivityOutput(ctx context.Context, scope string, resultJSON rawjson.Message, result *planner.ToolResult, pause *ToolPause, payload []byte) (*ToolOutput, error) {
	out := newToolActivityOutput(resultJSON, result)
	out.Pause = pause
	out.Preview = r.formatResultPreview(ctx, result.Name, nil, result.Result, result.Bounds)
	out.ResultBytes = len(resultJSON)
	if err := r.storeToolActivityContent(ctx, scope, out, payload); err != nil {
		return nil, err
	}
	out.Payload, out.ServerData = nil, nil
	return out, nil
}

// storeToolActivityContent publishes no references until all writes succeed.
func (r *Runtime) storeToolActivityContent(ctx context.Context, scope string, out *ToolOutput, payload []byte) error {
	if r.RunStore == nil {
		return fmt.Errorf("store tool content: run store is required")
	}
	var refs tools.ResultRefs
	for _, part := range []struct {
		name string
		data []byte
		hash *runstore.Hash
	}{
		{"result", out.Payload, &refs.Result},
		{"server data", out.ServerData, &refs.ServerData},
		{"executed payload", payload, &refs.Payload},
	} {
		if len(part.data) == 0 {
			continue
		}
		hash, err := r.RunStore.Put(ctx, scope, part.data)
		if err != nil {
			return fmt.Errorf("store tool %s: %w", part.name, err)
		}
		*part.hash = hash
	}
	out.Refs = refs
	return nil
}
