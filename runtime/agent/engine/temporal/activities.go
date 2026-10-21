package temporal

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	activity "github.com/CaliLuke/loom/execution/adapter"
	"go.temporal.io/sdk/workflow"
)

type activityFuture struct {
	future workflow.Future
	ctx    workflow.Context
}

// RegisterActivity installs a concrete typed handler in the Temporal worker.
func (e *Engine) RegisterActivity(_ context.Context, def engine.ActivityDefinition) error {
	if err := e.requireWorkerMode("register activities"); err != nil {
		return err
	}
	handler, err := activity.New(def.Handler)
	if err != nil {
		return err
	}
	opts := def.Options
	wrapped := handler.Wrap(e.injectWorkflowContextIntoActivity, classifyTemporalCancellation)
	return e.registerActivityWithCtx(def.Name, opts, wrapped)
}

func (w *temporalWorkflowContext) ExecuteActivity(ctx context.Context, call engine.ActivityCall, result any) error {
	future, err := w.ExecuteActivityAsync(ctx, call)
	if err != nil {
		return err
	}
	return future.Get(ctx, result)
}

func (w *temporalWorkflowContext) ExecuteActivityAsync(_ context.Context, call engine.ActivityCall) (engine.ActivityFuture, error) {
	if err := activity.ValidateCall(call.Name, call.Input); err != nil {
		return nil, err
	}
	actx := workflow.WithActivityOptions(w.ctx, w.activityOptionsFor(call.Name, call.Options))
	future := workflow.ExecuteActivity(actx, call.Name, call.Input)
	return activityFuture{future: future, ctx: actx}, nil
}

func (f activityFuture) Get(_ context.Context, result any) error {
	return normalizeTemporalError(f.future.Get(f.ctx, result))
}

func (f activityFuture) IsReady() bool {
	return f.future.IsReady()
}
