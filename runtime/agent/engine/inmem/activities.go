package inmem

import (
	"context"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	activity "github.com/CaliLuke/loom/execution/adapter"
)

type activityDef struct {
	handler activity.Handler
	opts    engine.ActivityOptions
}

type activityFuture struct{ *future[any] }

// RegisterActivity installs a handler without depending on agent payload types.
func (e *eng) RegisterActivity(_ context.Context, def engine.ActivityDefinition) error {
	if def.Name == "" {
		return errors.New("activity name is required")
	}
	handler, err := activity.New(def.Handler)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.activities == nil {
		e.activities = make(map[string]activityDef)
	}
	if _, dup := e.activities[def.Name]; dup {
		return fmt.Errorf("activity %q already registered", def.Name)
	}
	e.activities[def.Name] = activityDef{handler: handler, opts: def.Options}
	return nil
}

func (w *wfCtx) ExecuteActivity(ctx context.Context, call engine.ActivityCall, result any) error {
	def, err := w.findActivity(call)
	if err != nil {
		return err
	}
	actCtx, cancel := w.activityContext(ctx, call.Name, def.opts, call.Options)
	defer cancel()
	out, err := def.handler.Call(actCtx, call.Input)
	if assignErr := activity.Assign(result, out); assignErr != nil {
		return errors.Join(normalizeError(err), assignErr)
	}
	return normalizeError(err)
}

func (w *wfCtx) ExecuteActivityAsync(ctx context.Context, call engine.ActivityCall) (engine.ActivityFuture, error) {
	def, err := w.findActivity(call)
	if err != nil {
		return nil, err
	}
	actCtx, cancel := w.activityContext(ctx, call.Name, def.opts, call.Options)
	fut := &future[any]{ready: make(chan struct{})}
	go func() {
		defer close(fut.ready)
		defer cancel()
		fut.result, fut.err = def.handler.Call(actCtx, call.Input)
		fut.err = normalizeError(fut.err)
	}()
	return activityFuture{future: fut}, nil
}

func (f activityFuture) Get(ctx context.Context, result any) error {
	out, err := f.future.Get(ctx)
	if out == nil && err != nil {
		return err
	}
	if assignErr := activity.Assign(result, out); assignErr != nil {
		return errors.Join(err, assignErr)
	}
	return err
}

func (w *wfCtx) findActivity(call engine.ActivityCall) (activityDef, error) {
	if err := activity.ValidateCall(call.Name, call.Input); err != nil {
		return activityDef{}, err
	}
	w.eng.mu.RLock()
	def, ok := w.eng.activities[call.Name]
	w.eng.mu.RUnlock()
	if !ok {
		return activityDef{}, fmt.Errorf("activity %q not registered", call.Name)
	}
	return def, nil
}
