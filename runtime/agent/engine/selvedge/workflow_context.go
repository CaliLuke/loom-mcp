package selvedge

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	sv "github.com/CaliLuke/selvedge"
)

type workflowContext struct {
	engine.AgentWorkflowActivities
	engine *Engine
	ctx    *sv.WorkflowContext
}

func (w *workflowContext) Context() context.Context {
	return engine.WithWorkflowContext(context.WithoutCancel(w.ctx.Context()), w)
}

func (w *workflowContext) WorkflowID() string {
	return w.ctx.InstanceID()
}

func (w *workflowContext) RunID() string {
	return w.ctx.InstanceID()
}

func (w *workflowContext) SetQueryHandler(name string, handler any) error {
	if handler == nil {
		return sv.SetQueryHandler[any](w.ctx, name, nil)
	}
	value := reflect.ValueOf(handler)
	typ := value.Type()
	if typ.Kind() != reflect.Func || value.IsNil() || typ.NumIn() != 0 || typ.NumOut() != 2 || typ.Out(1) != reflect.TypeFor[error]() {
		return errors.New("selvedge: query handler must be func() (R, error)")
	}
	return sv.SetQueryHandler(w.ctx, name, func() (any, error) {
		result := value.Call(nil)
		if !result[1].IsNil() {
			return result[0].Interface(), result[1].Interface().(error)
		}
		return result[0].Interface(), nil
	})
}

func (w *workflowContext) Now() time.Time {
	value, err := sv.Now(w.ctx)
	if err != nil {
		return value
	}
	return value
}

func (w *workflowContext) Await(ctx context.Context, condition func() bool) error {
	if condition == nil {
		return errors.New("await condition is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if engine.WorkflowContextFromContext(ctx) != w {
		return errors.New("await context must come from this workflow context; use WithCancel().Context() for workflow-scope cancellation")
	}
	if ctx.Done() != nil {
		return errors.New("await context cannot use Go cancellation; use WithCancel().Context() for workflow-scope cancellation")
	}
	return normalizeError(sv.Await(w.ctx, condition))
}

func (w *workflowContext) WithCancel() (engine.WorkflowContext, func()) {
	scoped, cancel := sv.WithCancel(w.ctx)
	return newWorkflowContext(w.engine, scoped), cancel
}

func (w *workflowContext) Detached() engine.WorkflowContext {
	return newWorkflowContext(w.engine, sv.Detached(w.ctx))
}

func newWorkflowContext(e *Engine, ctx *sv.WorkflowContext) *workflowContext {
	w := &workflowContext{engine: e, ctx: ctx}
	w.AgentWorkflowActivities.ActivityExecutor = w
	return w
}
