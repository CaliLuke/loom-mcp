package temporal

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"go.temporal.io/sdk/workflow"
)

// executeRecordedActivity preserves typed payloads and the registered replay options.
func executeRecordedActivity[I, O any](w *temporalWorkflowContext, kind, name string, input *I, options engine.ActivityOptions) (*O, error) {
	if name == "" {
		return nil, fmt.Errorf("%s activity name is required", kind)
	}
	if input == nil {
		return nil, fmt.Errorf("%s activity input is required", kind)
	}
	actx := workflow.WithActivityOptions(w.ctx, w.activityOptionsFor(name, options))
	var out *O
	if err := workflow.ExecuteActivity(actx, name, input).Get(actx, &out); err != nil {
		return nil, normalizeTemporalError(err)
	}
	return out, nil
}

// registerRecordedActivity keeps worker context injection and cancellation classification uniform.
func registerRecordedActivity[I, O any](e *Engine, label string, kind activityKind, name string, opts engine.ActivityOptions, fn func(context.Context, *I) (*O, error)) error {
	if err := e.requireWorkerMode("register " + label + " activities"); err != nil {
		return err
	}
	opts = e.applyActivityClassDefaults(kind, opts)
	wrapped := func(ctx context.Context, in *I) (*O, error) {
		out, err := fn(e.injectWorkflowContextIntoActivity(ctx), in)
		return out, classifyTemporalCancellation(err)
	}
	return e.registerActivityWithCtx(name, opts, wrapped)
}
