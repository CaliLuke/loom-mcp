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

// ToolAwaitNotReadyError identifies a submission whose await is not the current
// tool-channel head. The ID can also be unknown, already accepted, or consumed.
// Callers must wait until QueryToolAwaits advertises this ID before resubmitting.
type ToolAwaitNotReadyError struct {
	ID string
}

// Error describes the unavailable tool await.
func (e *ToolAwaitNotReadyError) Error() string {
	return fmt.Sprintf("await %q is not yet awaited or is no longer pending", e.ID)
}

// validateProvidedToolResults materializes and validates before content writes
// or signal delivery. Artifact persistence remains activity-owned.
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
			if err := r.prepareToolResult(ctx, spec, call, result); err != nil {
				return err
			}
		}
		return nil
	}
	err = &ToolAwaitNotReadyError{ID: rs.ID}
	return engine.NewNonRetryableApplicationError(err.Error(), ToolResultContractErrorType, nil, err)
}

// setToolAwaitsQuery publishes only the first remaining tool-channel await.
// Other signal channels do not block it. Replacing the handler
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
			await = planner.AwaitExternalTools{ID: q.ID, Items: []planner.AwaitToolItem{{Name: q.ToolName, ToolCallID: q.ToolCallID, Payload: slices.Clone(q.Payload)}}}
		case planner.AwaitItemKindExternalTools:
			if item.ExternalTools == nil {
				return fmt.Errorf("await external_tools item %d missing payload", i)
			}
			await.ID = item.ExternalTools.ID
			for _, call := range item.ExternalTools.Items {
				await.Items = append(await.Items, planner.AwaitToolItem{Name: call.Name, ToolCallID: call.ToolCallID, Payload: slices.Clone(call.Payload)})
			}
		case planner.AwaitItemKindClarification, planner.AwaitItemKindTypedInput:
			continue
		default:
			continue
		}
		awaits = append(awaits, await)
		break
	}
	results := wf.ExternalToolResults()
	return wf.SetQueryHandler(engine.QueryToolAwaits, func() ([]planner.AwaitExternalTools, error) {
		// A delivered answer already owns the head until the workflow takes it.
		// Query and signal are not atomic; consumption deduplicates racing callers.
		if results.Pending() > 0 {
			return nil, nil
		}
		out := slices.Clone(awaits)
		for i := range out {
			out[i].Items = slices.Clone(out[i].Items)
			for j := range out[i].Items {
				out[i].Items[j].Payload = slices.Clone(out[i].Items[j].Payload)
			}
		}
		return out, nil
	})
}
