package runtime

import (
	"context"
	"encoding/json/jsontext"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/hooks"
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
	if err := r.recordProvidedResults(batchCtx, results, data); err != nil {
		return nil, err
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
	if err := r.publishProvidedToolResults(ctx, p, results, data); err != nil {
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
	result := &planner.ToolResult{Name: event.Name, ToolCallID: event.ToolCallID, Bounds: event.Bounds, Error: event.Error, RetryHint: event.RetryHint, Telemetry: event.Telemetry, Artifacts: artifactContentsFromRefs(event.Artifacts), Preview: event.Preview, Refs: event.Refs}
	exec := &toolBatchExec{r: r, agentID: p.AgentID, runID: p.RunContext.RunID, sessionID: p.RunContext.SessionID, turnID: p.RunContext.TurnID, runCtx: &p.RunContext}
	duration := time.Duration(p.Duration)
	var data []byte
	if event.Refs.Result != "" && event.ResultBytes <= maxHookPayloadBytes {
		var err error
		data, err = r.RunStore.Get(ctx, root, event.Refs.Result)
		if err != nil {
			return err
		}
		if len(data) > maxHookPayloadBytes {
			data = nil
		} else if hasNonNullJSON(data) && event.Error == nil {
			result.Result, err = r.unmarshalToolValue(ctx, event.Name, data, false)
			if err != nil {
				return err
			}
		}
	}
	// The result's typed and canonical representations both count toward the
	// hook bound. Once either the known size or encoded input exceeds it, do
	// not load further content solely for this observer publication.
	in, err := hooks.EncodeToHookInput(exec.toolResultEvent(ctx, p.Call, result, rawjson.Message(data), duration), exec.turnID)
	if err != nil {
		return err
	}
	if event.ResultBytes > maxHookPayloadBytes || event.Refs.Result != "" && len(data) == 0 || len(in.Payload) > maxHookPayloadBytes {
		result.Result, data = nil, nil
		result.Preview = omittedResultPreview(result.Preview)
	} else if event.Refs.ServerData != "" {
		result.ServerData, err = r.RunStore.Get(ctx, root, event.Refs.ServerData)
		if err != nil {
			return err
		}
	}
	return exec.publishToolResultEvent(ctx, p.Call, result, rawjson.Message(data), duration)
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

// recordProvidedResults sorts object keys before assigning retry-stable content refs.
func (r *Runtime) recordProvidedResults(batchCtx context.Context, results []*planner.ToolResult, data []rawjson.Message) error {
	for i, result := range results {
		// Materialized JSON can contain maps. Stable bytes keep content refs and
		// retry publication bodies identical without rounding JSON numbers.
		if len(data[i]) > 0 {
			value := jsontext.Value(data[i])
			if err := value.Format(jsontext.ReorderRawObjects(true)); err != nil {
				return err
			}
			data[i] = rawjson.Message(value)
		}
		if err := r.recordWorkflowToolResult(batchCtx, result, data[i]); err != nil {
			return err
		}
	}
	return nil
}
