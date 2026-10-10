package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/model"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

const maxFinalToolResultBytes = 1_000_000

// FinishRunActivityName returns the terminal activity name registered for an agent.
func FinishRunActivityName(id agent.Ident) string {
	return string(id) + ".finish"
}

// FinishRunActivity stores the event list in the root scope and resolves the
// selected final result with its generated codec. RegisterAgent registers this
// activity automatically on the agent's workflow queue.
func (r *Runtime) FinishRunActivity(ctx context.Context, input *api.FinishRunInput) (*api.FinishRunOutput, error) {
	if input == nil || input.RootRunID == "" {
		return nil, fmt.Errorf("finish run: root run id is required")
	}
	if r.RunStore == nil {
		return nil, fmt.Errorf("finish run: run store is required")
	}
	data, err := json.Marshal(input.ToolEvents)
	if err != nil {
		return nil, fmt.Errorf("encode run tool events: %w", err)
	}
	ref, err := r.RunStore.Put(ctx, input.RootRunID, data)
	if err != nil {
		return nil, fmt.Errorf("store run tool events: %w", err)
	}
	final, err := r.finishToolResult(ctx, input.RootRunID, input.FinalToolResult)
	if err != nil {
		return nil, err
	}
	var finalRef runstore.Hash
	if input.Final != nil {
		data, err := json.Marshal(input.Final)
		if err != nil {
			return nil, fmt.Errorf("encode final message: %w", err)
		}
		finalRef, err = r.RunStore.Put(ctx, input.RootRunID, data)
		if err != nil {
			return nil, fmt.Errorf("store final message: %w", err)
		}
	}
	summary := summarizeChildToolEvents(input.ToolEvents)
	return &api.FinishRunOutput{
		ToolEventsRef:  ref,
		FinalRef:       finalRef,
		ToolEventCount: len(input.ToolEvents),
		ToolSummary: api.ToolEventSummary{
			FailedCount: summary.failedCount,
			LastError:   summary.lastError,
			Telemetry:   summary.asTelemetry(),
		},
		FinalToolResult: final,
	}, nil
}

// finishRun schedules terminal storage in a detached scope so cancellation does
// not prevent the existing terminal-output paths from retaining their events.
func (r *Runtime) finishRun(ctx context.Context, input *RunInput, events []*api.ToolEvent, final *api.ToolEvent, message *model.Message) (*api.FinishRunOutput, error) {
	wf := engine.WorkflowContextFromContext(ctx)
	if wf == nil {
		return nil, fmt.Errorf("finish run: workflow context is required")
	}
	wf = wf.Detached()
	return wf.ExecuteFinishRunActivity(wf.Context(), engine.FinishRunActivityCall{
		Name:  FinishRunActivityName(input.AgentID),
		Input: &api.FinishRunInput{RootRunID: input.RootRunID, ToolEvents: events, FinalToolResult: final, Final: message},
	})
}

func (r *Runtime) finishToolResult(ctx context.Context, scope string, event *api.ToolEvent) (*api.ToolEvent, error) {
	if event == nil {
		return nil, nil
	}
	final := *event
	if event.Refs.Result != "" {
		spec, ok := r.toolSpec(event.Name)
		if !ok {
			return nil, fmt.Errorf("finish tool %s: result spec not found", event.Name)
		}
		value, err := LoadToolResult(ctx, r.RunStore, scope, spec, event.Refs.Result)
		if err != nil {
			return nil, fmt.Errorf("finish tool result: %w", err)
		}
		data, err := r.marshalToolValue(ctx, event.Name, value, event.Bounds)
		if err != nil {
			return nil, fmt.Errorf("encode final tool result: %w", err)
		}
		final.Result = rawjson.Message(data)
		final.ResultBytes = len(data)
	}
	if event.Refs.ServerData != "" {
		data, err := r.RunStore.Get(ctx, scope, event.Refs.ServerData)
		if err != nil {
			return nil, fmt.Errorf("load final tool server data: %w", err)
		}
		final.ServerData = rawjson.Message(data)
	}
	data, err := json.Marshal(&final)
	if err != nil {
		return nil, fmt.Errorf("encode final tool result: %w", err)
	}
	if len(data) > maxFinalToolResultBytes {
		return nil, fmt.Errorf("final tool result too large: %d bytes (limit %d)", len(data), maxFinalToolResultBytes)
	}
	return &final, nil
}
