package temporal

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
)

// RegisterSummarizeContextActivity registers recorded summary folding on the agent queue.
func (e *Engine) RegisterSummarizeContextActivity(_ context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.SummarizeContextInput) (*api.SummarizeContextOutput, error)) error {
	return registerRecordedActivity(e, "summary", activityKindPlanner, name, opts, fn)
}

// ExecuteSummarizeContextActivity schedules summary folding with replayed results and options.
func (w *temporalWorkflowContext) ExecuteSummarizeContextActivity(ctx context.Context, call engine.SummarizeContextActivityCall) (*api.SummarizeContextOutput, error) {
	return executeRecordedActivity[api.SummarizeContextInput, api.SummarizeContextOutput](w, "summary", call.Name, call.Input, call.Options)
}
