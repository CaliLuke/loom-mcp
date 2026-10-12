package selvedge

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	sv "github.com/CaliLuke/selvedge"
)

type workflowContext struct {
	engine *Engine
	ctx    *sv.WorkflowContext
}

func executeActivity[I, O any](w *workflowContext, registry map[string]*sv.Activity[*I, O], name string, input *I) (*sv.Future[O], error) {
	if name == "" {
		return nil, errors.New("activity name is required")
	}
	if input == nil {
		return nil, errors.New("activity input is required")
	}
	w.engine.mu.Lock()
	definition := registry[name]
	w.engine.mu.Unlock()
	if definition == nil {
		return nil, fmt.Errorf("selvedge: activity %q is not registered", name)
	}
	future, err := definition.ExecuteAsync(w.ctx, input)
	return future, normalizeError(err)
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
	return &workflowContext{engine: w.engine, ctx: scoped}, cancel
}

func (w *workflowContext) Detached() engine.WorkflowContext {
	return &workflowContext{engine: w.engine, ctx: sv.Detached(w.ctx)}
}

func (w *workflowContext) PublishHook(_ context.Context, call engine.HookActivityCall) error {
	future, err := executeActivity(w, w.engine.activities.hook, call.Name, call.Input)
	if err != nil {
		return err
	}
	_, err = future.Get(w.ctx)
	return normalizeError(err)
}

func (w *workflowContext) ExecutePlannerActivity(_ context.Context, call engine.PlannerActivityCall) (*api.PlanActivityOutput, error) {
	future, err := executeActivity(w, w.engine.activities.planner, call.Name, call.Input)
	if err != nil {
		return nil, err
	}
	out, err := future.Get(w.ctx)
	return out, normalizeError(err)
}

func (w *workflowContext) ExecuteToolActivity(_ context.Context, call engine.ToolActivityCall) (*api.ToolOutput, error) {
	future, err := executeActivity(w, w.engine.activities.tool, call.Name, call.Input)
	if err != nil {
		return nil, err
	}
	out, err := future.Get(w.ctx)
	return out, normalizeError(err)
}

func (w *workflowContext) ExecuteSealRecordActivity(_ context.Context, call engine.SealRecordActivityCall) (*api.SealRecordOutput, error) {
	future, err := executeActivity(w, w.engine.activities.sealRecord, call.Name, call.Input)
	if err != nil {
		return nil, err
	}
	out, err := future.Get(w.ctx)
	return out, normalizeError(err)
}

func (w *workflowContext) ExecuteFinishRunActivity(_ context.Context, call engine.FinishRunActivityCall) (*api.FinishRunOutput, error) {
	future, err := executeActivity(w, w.engine.activities.finishRun, call.Name, call.Input)
	if err != nil {
		return nil, err
	}
	out, err := future.Get(w.ctx)
	return out, normalizeError(err)
}

func (w *workflowContext) ExecuteRecordContentActivity(_ context.Context, call engine.RecordContentActivityCall) (*api.RecordContentOutput, error) {
	future, err := executeActivity(w, w.engine.activities.recordContent, call.Name, call.Input)
	if err != nil {
		return nil, err
	}
	out, err := future.Get(w.ctx)
	return out, normalizeError(err)
}

func (w *workflowContext) ExecuteSummarizeContextActivity(_ context.Context, call engine.SummarizeContextActivityCall) (*api.SummarizeContextOutput, error) {
	future, err := executeActivity(w, w.engine.activities.summarizeContext, call.Name, call.Input)
	if err != nil {
		return nil, err
	}
	out, err := future.Get(w.ctx)
	return out, normalizeError(err)
}

func (w *workflowContext) ExecuteRecallActivity(_ context.Context, call engine.RecallActivityCall) (*api.RecallActivityOutput, error) {
	future, err := executeActivity(w, w.engine.activities.recall, call.Name, call.Input)
	if err != nil {
		return nil, err
	}
	out, err := future.Get(w.ctx)
	return out, normalizeError(err)
}

func (w *workflowContext) ExecuteChildDispatchActivity(_ context.Context, call engine.ChildDispatchActivityCall) (*api.ChildDispatchOutput, error) {
	future, err := executeActivity(w, w.engine.activities.childDispatch, call.Name, call.Input)
	if err != nil {
		return nil, err
	}
	out, err := future.Get(w.ctx)
	return out, normalizeError(err)
}

func (w *workflowContext) ExecuteToolActivityAsync(_ context.Context, call engine.ToolActivityCall) (engine.Future[*api.ToolOutput], error) {
	future, err := executeActivity(w, w.engine.activities.tool, call.Name, call.Input)
	if err != nil {
		return nil, err
	}
	return &activityFuture[*api.ToolOutput]{ctx: w.ctx, future: future}, nil
}
