package temporal

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
)

// RegisterSealRecordActivity registers terminal transcript sealing on the agent queue.
func (e *Engine) RegisterSealRecordActivity(_ context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.SealRecordInput) (*api.SealRecordOutput, error)) error {
	return registerRecordedActivity(e, "seal", activityKindHook, name, opts, fn)
}

// ExecuteSealRecordActivity schedules sealing with replayed results and options.
func (w *temporalWorkflowContext) ExecuteSealRecordActivity(ctx context.Context, call engine.SealRecordActivityCall) (*api.SealRecordOutput, error) {
	return executeRecordedActivity[api.SealRecordInput, api.SealRecordOutput](w, "seal", call.Name, call.Input, call.Options)
}
