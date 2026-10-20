package selvedge

import (
	"context"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	sv "github.com/CaliLuke/selvedge"
)

type timerFuture struct {
	ctx   *sv.WorkflowContext
	timer *sv.Timer
}

type readyTimer struct {
	value time.Time
}

type childHandle struct {
	ctx   *sv.WorkflowContext
	child *sv.Child[*api.RunOutput]
}

func (f *timerFuture) Get(context.Context) (time.Time, error) {
	out, err := f.timer.Get(f.ctx)
	return out, normalizeError(err)
}

func (f *timerFuture) IsReady() bool {
	return f.timer.IsReady()
}

func (f readyTimer) Get(context.Context) (time.Time, error) {
	return f.value, nil
}

func (f readyTimer) IsReady() bool {
	return true
}

func (w *workflowContext) NewTimer(_ context.Context, d time.Duration) (engine.Future[time.Time], error) {
	timer, err := sv.NewTimer(w.ctx, d)
	if err != nil {
		return nil, normalizeError(err)
	}
	if d <= 0 {
		value, err := timer.Get(w.ctx)
		if err != nil {
			return nil, normalizeError(err)
		}
		return readyTimer{value: value}, nil
	}
	return &timerFuture{ctx: w.ctx, timer: timer}, nil
}

func (w *workflowContext) StartChildWorkflow(_ context.Context, req engine.ChildWorkflowRequest) (engine.ChildWorkflowHandle, error) {
	child, err := sv.StartChild[*api.RunOutput](w.ctx, req.Workflow, req.Input, sv.WithChildID(req.ID))
	if err != nil {
		return nil, startError(normalizeError(err))
	}
	return &childHandle{ctx: w.ctx, child: child}, nil
}

func (c *childHandle) Get(context.Context) (*api.RunOutput, error) {
	out, err := c.child.Get(c.ctx)
	return out, startError(normalizeError(err))
}

func (c *childHandle) IsReady() bool {
	return c.child.IsReady()
}

func (c *childHandle) Cancel(context.Context) error {
	return normalizeError(c.child.Cancel(c.ctx))
}

func (c *childHandle) RunID() string {
	return c.child.ID()
}
