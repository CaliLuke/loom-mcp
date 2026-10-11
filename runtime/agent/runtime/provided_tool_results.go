package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/planner"
)

// validateProvidedToolResults performs read-only preflight before content writes
// or signal delivery. Materialization and artifact persistence remain activity-owned.
func (r *Runtime) validateProvidedToolResults(ctx context.Context, rs *api.ToolResultsSet) error {
	status, err := r.Engine.QueryRunStatus(ctx, rs.RunID)
	if err != nil {
		return err
	}
	if isTerminalRunStatus(status) {
		return engine.ErrWorkflowCompleted
	}
	querier, ok := r.Engine.(engine.ToolAwaitQuerier)
	if !ok {
		return errors.New("engine does not support pending tool await queries")
	}
	awaits, err := querier.QueryToolAwaits(ctx, rs.RunID)
	if err != nil {
		return err
	}
	for _, await := range awaits {
		if await.ID != rs.ID {
			continue
		}
		calls, expected, err := buildExternalAwaitRequests(await.Items)
		if err != nil {
			return err
		}
		provided, err := indexProvidedToolResults(rs, expected)
		if err != nil {
			return err
		}
		for _, call := range calls {
			item := provided[call.ToolCallID]
			spec, err := r.providedToolSpec(call, item)
			if err != nil {
				return err
			}
			result, err := decodeProvidedToolResultValue(spec, call, item)
			if err != nil {
				return err
			}
			if err := validateToolResultContract(spec, call, result); err != nil {
				return err
			}
		}
		return nil
	}
	return toolResultContractErrorf("await: no pending tool await matches id %q", rs.ID)
}

// setToolAwaitsQuery publishes immutable ID/name snapshots. Replacing the handler
// advances pending state without adding workflow commands or racing in-memory queries.
func setToolAwaitsQuery(wf engine.WorkflowContext, items []planner.AwaitItem) error {
	awaits := make([]planner.AwaitExternalTools, 0, len(items))
	for i, item := range items {
		var await planner.AwaitExternalTools
		switch item.Kind {
		case planner.AwaitItemKindQuestions:
			q := item.Questions
			if q == nil {
				return fmt.Errorf("await questions item %d missing payload", i)
			}
			await = planner.AwaitExternalTools{ID: q.ID, Items: []planner.AwaitToolItem{{Name: q.ToolName, ToolCallID: q.ToolCallID}}}
		case planner.AwaitItemKindExternalTools:
			if item.ExternalTools == nil {
				return fmt.Errorf("await external_tools item %d missing payload", i)
			}
			await.ID = item.ExternalTools.ID
			for _, call := range item.ExternalTools.Items {
				await.Items = append(await.Items, planner.AwaitToolItem{Name: call.Name, ToolCallID: call.ToolCallID})
			}
		case planner.AwaitItemKindClarification, planner.AwaitItemKindTypedInput:
			continue
		default:
			continue
		}
		awaits = append(awaits, await)
	}
	return wf.SetQueryHandler(engine.QueryToolAwaits, func() ([]planner.AwaitExternalTools, error) {
		out := slices.Clone(awaits)
		for i := range out {
			out[i].Items = slices.Clone(out[i].Items)
		}
		return out, nil
	})
}
