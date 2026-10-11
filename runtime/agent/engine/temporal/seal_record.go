package temporal

import (
	"context"
	"errors"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"go.temporal.io/sdk/workflow"
)

// RegisterSealRecordActivity registers terminal transcript sealing on the agent queue.
func (e *Engine) RegisterSealRecordActivity(_ context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.SealRecordInput) (*api.SealRecordOutput, error)) error {
	if err := e.requireWorkerMode("register seal activities"); err != nil {
		return err
	}
	opts = e.applyActivityClassDefaults(activityKindHook, opts)
	wrapped := func(ctx context.Context, in *api.SealRecordInput) (*api.SealRecordOutput, error) {
		out, err := fn(e.injectWorkflowContextIntoActivity(ctx), in)
		return out, classifyTemporalCancellation(err)
	}
	return e.registerActivityWithCtx(name, opts, wrapped)
}

// ExecuteSealRecordActivity schedules sealing with replayed results and options.
func (w *temporalWorkflowContext) ExecuteSealRecordActivity(ctx context.Context, call engine.SealRecordActivityCall) (*api.SealRecordOutput, error) {
	if call.Name == "" {
		return nil, errors.New("seal activity name is required")
	}
	if call.Input == nil {
		return nil, errors.New("seal activity input is required")
	}
	actx := workflow.WithActivityOptions(w.ctx, w.activityOptionsFor(call.Name, call.Options))
	var out *api.SealRecordOutput
	if err := workflow.ExecuteActivity(actx, call.Name, call.Input).Get(actx, &out); err != nil {
		return nil, normalizeTemporalError(err)
	}
	return out, nil
}
