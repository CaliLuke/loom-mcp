package selvedge

import (
	"context"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	sv "github.com/CaliLuke/selvedge"
)

type receiver[T any] struct {
	ctx     *sv.WorkflowContext
	channel string
}

func (w *workflowContext) PauseRequests() engine.Receiver[*api.PauseRequest] {
	return &receiver[*api.PauseRequest]{ctx: w.ctx, channel: api.SignalPause}
}

func (w *workflowContext) ResumeRequests() engine.Receiver[*api.ResumeRequest] {
	return &receiver[*api.ResumeRequest]{ctx: w.ctx, channel: api.SignalResume}
}

func (w *workflowContext) ClarificationAnswers() engine.Receiver[*api.ClarificationAnswer] {
	return &receiver[*api.ClarificationAnswer]{ctx: w.ctx, channel: api.SignalProvideClarification}
}

func (w *workflowContext) ExternalToolResults() engine.Receiver[*api.ToolResultsSet] {
	return &receiver[*api.ToolResultsSet]{ctx: w.ctx, channel: api.SignalProvideToolResults}
}

func (w *workflowContext) ConfirmationDecisions() engine.Receiver[*api.ConfirmationDecision] {
	return &receiver[*api.ConfirmationDecision]{ctx: w.ctx, channel: api.SignalProvideConfirmation}
}

func (w *workflowContext) TypedInputAnswers() engine.Receiver[*api.TypedInputAnswer] {
	return &receiver[*api.TypedInputAnswer]{ctx: w.ctx, channel: api.SignalProvideTypedInput}
}

func (r *receiver[T]) Receive(context.Context) (T, error) {
	value, err := sv.Receive[T](r.ctx, r.channel)
	return value, normalizeError(err)
}

func (r *receiver[T]) ReceiveWithTimeout(_ context.Context, timeout time.Duration) (T, error) {
	value, err := sv.ReceiveWithTimeout[T](r.ctx, r.channel, timeout)
	return value, normalizeError(err)
}

func (r *receiver[T]) ReceiveAsync() (T, bool) {
	value, received, err := sv.ReceiveAsync[T](r.ctx, r.channel)
	if err != nil {
		var zero T
		return zero, false
	}
	return value, received
}

func (r *receiver[T]) Pending() int {
	return sv.Pending(r.ctx, r.channel)
}
