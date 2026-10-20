package selvedge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/internal/activity"
	sv "github.com/CaliLuke/selvedge"
)

type activityResult struct {
	ctx    *sv.WorkflowContext
	future *sv.Future[json.RawMessage]
}

func (w *workflowContext) ExecuteActivity(ctx context.Context, call engine.ActivityCall, result any) error {
	future, err := w.ExecuteActivityAsync(ctx, call)
	if err != nil {
		return err
	}
	return future.Get(ctx, result)
}

func (w *workflowContext) ExecuteActivityAsync(_ context.Context, call engine.ActivityCall) (engine.ActivityFuture, error) {
	if err := activity.ValidateCall(call.Name, call.Input); err != nil {
		return nil, err
	}
	w.engine.mu.Lock()
	definition := w.engine.activities[call.Name]
	w.engine.mu.Unlock()
	if definition == nil {
		return nil, fmt.Errorf("selvedge: activity %q is not registered", call.Name)
	}
	input, err := json.Marshal(call.Input)
	if err != nil {
		return nil, err
	}
	future, err := definition.ExecuteAsync(w.ctx, json.RawMessage(input), sv.WithActivityOptions(invocationActivityOptions(call.Options)))
	if err != nil {
		return nil, normalizeError(err)
	}
	return activityResult{ctx: w.ctx, future: future}, nil
}

func (f activityResult) Get(_ context.Context, result any) error {
	out, err := f.future.Get(f.ctx)
	if err != nil {
		return normalizeError(err)
	}
	return json.Unmarshal(out, result)
}

func (f activityResult) IsReady() bool {
	return f.future.IsReady()
}
