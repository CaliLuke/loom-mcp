package runtime

import (
	"context"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/rawjson"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
)

// recordProvidedContent resolves and validates signals inside an activity.
func (r *Runtime) recordProvidedContent(ctx context.Context, input *api.RecordContentInput) (*api.RecordContentOutput, error) {
	p := input.Provided
	results, data, err := r.loadProvidedContent(ctx, input.RootRunID, p)
	if err != nil {
		return nil, err
	}
	batch := &workflowContentBatch{seen: make(map[runstore.Hash]bool)}
	// Signal content is already stored; reuse an identical materialized result.
	for _, item := range p.Results {
		batch.seen[item.Refs.Result] = true
	}
	batchCtx := context.WithValue(ctx, workflowContentKey{}, batch)
	for i, result := range results {
		if err := r.recordWorkflowToolResult(batchCtx, result, data[i]); err != nil {
			return nil, err
		}
	}
	base := &workflowPlanInput{AgentID: p.AgentID, RunContext: p.RunContext, CallRefs: p.CallRefs}
	entries, err := r.buildIndexResultEntries(batchCtx, base, p.Calls, results, false, p.First)
	if err != nil {
		return nil, err
	}
	stored, err := r.recordContentItems(ctx, batch.input(input.RootRunID))
	if err != nil {
		return nil, err
	}
	if err := batch.verify(stored); err != nil {
		return nil, err
	}
	if err := r.publishProvidedToolResults(ctx, &RunInput{AgentID: p.AgentID}, base, p.RunContext.TurnID, results, data); err != nil {
		return nil, err
	}
	events, err := r.encodeToolEvents(ctx, results)
	if err != nil {
		return nil, err
	}
	return &api.RecordContentOutput{ToolResults: events, Entries: entries}, nil
}

func (r *Runtime) publishStoredToolContent(ctx context.Context, root string, p *api.PublishToolContent) error {
	event := p.Result
	data, err := r.RunStore.Get(ctx, root, event.Refs.Result)
	if err != nil {
		return err
	}
	var value any
	if hasNonNullJSON(data) && event.Error == nil {
		value, err = r.unmarshalToolValue(ctx, event.Name, data, false)
		if err != nil {
			return err
		}
	}
	var serverData []byte
	if event.Refs.ServerData != "" {
		serverData, err = r.RunStore.Get(ctx, root, event.Refs.ServerData)
		if err != nil {
			return err
		}
	}
	result := &planner.ToolResult{Name: event.Name, ToolCallID: event.ToolCallID, Result: value, ServerData: serverData, Bounds: event.Bounds, Error: event.Error, RetryHint: event.RetryHint, Telemetry: event.Telemetry, Artifacts: artifactContentsFromRefs(event.Artifacts), Preview: event.Preview, Refs: event.Refs}
	exec := &toolBatchExec{r: r, agentID: p.AgentID, runID: p.RunContext.RunID, sessionID: p.RunContext.SessionID, turnID: p.RunContext.TurnID, runCtx: &p.RunContext}
	if err := exec.publishToolResultEvent(ctx, p.Call, result, rawjson.Message(data), time.Duration(p.Duration)); err != nil {
		return err
	}
	return nil
}

// loadProvidedContent validates and materializes reference-only signal results.
func (r *Runtime) loadProvidedContent(ctx context.Context, root string, p *api.ProvidedContent) ([]*planner.ToolResult, []rawjson.Message, error) {
	provided := make(map[string]*api.ProvidedToolResult, len(p.Results))
	for _, item := range p.Results {
		copy := *item
		data, err := r.RunStore.Get(ctx, root, item.Refs.Result)
		if err != nil {
			return nil, nil, err
		}
		copy.Result = rawjson.Message(data)
		provided[item.ToolCallID] = &copy
	}
	return r.decodeProvidedToolResults(ctx, p.Calls, provided)
}
