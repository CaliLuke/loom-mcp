package runtime

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"unicode/utf8"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/runstore"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/transcript/index"
)

// appendIndexResults records a dispatch-ordered batch. Metadata is itself
// content-addressed, so a workflow entry never grows with a result or retry hint.
func (r *Runtime) appendIndexResults(ctx context.Context, base *workflowPlanInput, calls []planner.ToolRequest, results []*planner.ToolResult, denial bool) error {
	if base.content == nil {
		base.content = &workflowContentBatch{}
	}
	batch := base.content
	batchCtx := context.WithValue(ctx, workflowContentKey{}, batch)
	entries, err := r.buildIndexResultEntries(batchCtx, base, calls, results, denial, index.MessageID(len(base.Entries)+1))
	if err != nil {
		return err
	}
	if err := batch.flush(ctx, base.AgentID, base.RunContext.RootRunID); err != nil {
		return err
	}
	if err := adoptResultEntries(base, entries, results); err != nil {
		return err
	}
	base.content = nil
	return nil
}

// buildIndexResultEntries prepares a batch without mutating the committed index.
func (r *Runtime) buildIndexResultEntries(ctx context.Context, base *workflowPlanInput, calls []planner.ToolRequest, results []*planner.ToolResult, denial bool, first index.MessageID) ([]index.MessageEntry, error) {
	byID, err := indexToolResultsByCallID(results)
	if err != nil {
		return nil, err
	}
	var entries []index.MessageEntry
	if hidden := r.hiddenBookkeepingIDs(calls, byID); len(hidden) > 0 {
		entries = append(entries, structuralIndexEntry(index.HideBookkeeping, hidden))
	}
	batch := first + index.MessageID(len(entries))
	var reminders []index.TextParameter
	for _, call := range calls {
		result := byID[call.ToolCallID]
		if result == nil {
			return nil, fmt.Errorf("missing result for call %s", call.ToolCallID)
		}
		entry, err := r.resultIndexEntry(ctx, base, call, result, batch, denial)
		if err != nil {
			return nil, err
		}
		params, err := r.indexResultReminders(ctx, result)
		if err != nil {
			return nil, err
		}
		reminders = append(reminders, params...)
		entries = append(entries, entry)
	}
	if len(reminders) > 0 {
		entries = append(entries, structuralIndexEntry(index.Reminders, nil, reminders...))
	}
	return entries, nil
}

func (r *Runtime) resultIndexEntry(ctx context.Context, base *workflowPlanInput, call planner.ToolRequest, result *planner.ToolResult, batch index.MessageID, denial bool) (index.MessageEntry, error) {
	if result.Refs.Result == "" && result.Error == nil {
		result.Preview = r.formatResultPreview(ctx, result.Name, nil, result.Result, result.Bounds)
		if err := r.recordWorkflowToolResult(ctx, result, nil); err != nil {
			return index.MessageEntry{}, err
		}
	}
	preview, err := workflowText(ctx, result.Preview)
	if err != nil {
		return index.MessageEntry{}, err
	}
	entry := index.MessageEntry{Kind: index.ToolResult, Hash: result.Refs.Result, Batch: batch, ToolUseIDs: []string{call.ToolCallID}, Bytes: result.ResultBytes, Result: &index.ResultProjection{Preview: preview, Bounds: result.Bounds}}
	if result.Error != nil {
		param, err := workflowText(ctx, result.Error.Error())
		if err != nil {
			return entry, err
		}
		entry.Kind, entry.Hash = index.Structural, ""
		entry.Structural = &index.StructuralEntry{Kind: index.SynthesizedError, ProtocolVersion: index.TemplateProtocolVersion, Text: []index.TextParameter{param}}
	} else if denial {
		entry.Kind, entry.Hash = index.Structural, ""
		entry.Structural = &index.StructuralEntry{Kind: index.ConfirmationDenial, ProtocolVersion: index.TemplateProtocolVersion, Text: []index.TextParameter{{Hash: result.Refs.Result}}}
	}
	if result.Refs.ServerData != "" {
		entry.Record = append(entry.Record, result.Refs.ServerData)
	}
	if result.Refs.Payload != "" {
		entry.Record = append(entry.Record, result.Refs.Payload)
	}
	if result.Error == nil && r.isBookkeeping(result.Name) {
		return entry, nil
	}
	entry.Output, err = r.storeIndexToolOutput(ctx, base, call, result)
	return entry, err
}

func truncateResultPreview(text string) string {
	const limit = 1024
	if len(text) <= limit {
		return text
	}
	text = text[:limit]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

func (r *Runtime) hiddenBookkeepingIDs(calls []planner.ToolRequest, byID map[string]*planner.ToolResult) []string {
	var hidden []string
	for _, call := range calls {
		result := byID[call.ToolCallID]
		if result != nil && result.Error == nil && r.isBookkeeping(result.Name) {
			hidden = append(hidden, call.ToolCallID)
		}
	}
	return hidden
}

func (r *Runtime) indexResultReminders(ctx context.Context, result *planner.ToolResult) ([]index.TextParameter, error) {
	if result.Error == nil && r.isBookkeeping(result.Name) {
		return nil, nil
	}
	var params []index.TextParameter
	for _, text := range r.toolResultReminders(result) {
		param, err := workflowText(ctx, text)
		if err != nil {
			return nil, err
		}
		params = append(params, param)
	}
	return params, nil
}

// storeIndexToolOutput separates bounded, loadable planner metadata from results.
func (r *Runtime) storeIndexToolOutput(ctx context.Context, base *workflowPlanInput, call planner.ToolRequest, result *planner.ToolResult) (runstore.Hash, error) {
	output, err := r.buildPlannerToolOutput(ctx, call, map[string]*planner.ToolResult{call.ToolCallID: result})
	if err != nil {
		return "", err
	}
	if output.Refs.Payload == "" {
		output.Call = base.CallRefs[call.ToolCallID]
		if output.Call == "" {
			return "", fmt.Errorf("missing stored call %s", call.ToolCallID)
		}
	}
	output.Result, output.ServerData, output.Payload = nil, nil, nil
	output.Preview = truncateResultPreview(result.Preview)
	data, err := json.Marshal(output, json.Deterministic(true))
	if err != nil {
		return "", err
	}
	refs, err := recordWorkflowContent(ctx, [][]byte{data})
	if err != nil {
		return "", err
	}
	return refs[0], nil
}

func structuralIndexEntry(kind index.StructuralKind, ids []string, text ...index.TextParameter) index.MessageEntry {
	return index.MessageEntry{Kind: index.Structural, ToolUseIDs: ids, Structural: &index.StructuralEntry{Kind: kind, ProtocolVersion: index.TemplateProtocolVersion, Text: text}}
}

func adoptResultEntries(base *workflowPlanInput, entries []index.MessageEntry, results []*planner.ToolResult) error {
	for _, entry := range entries {
		if err := base.appendEntry(entry); err != nil {
			return err
		}
	}
	for _, result := range results {
		result.Result, result.ServerData = nil, nil
	}
	return nil
}
