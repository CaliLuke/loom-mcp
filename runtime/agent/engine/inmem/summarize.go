package inmem

import (
	"context"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
)

type summaryActivityDef struct {
	handler func(context.Context, *api.SummarizeContextInput) (*api.SummarizeContextOutput, error)
	opts    engine.ActivityOptions
}

// RegisterSummarizeContextActivity registers recorded summary folding.
func (e *eng) RegisterSummarizeContextActivity(_ context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.SummarizeContextInput) (*api.SummarizeContextOutput, error)) error {
	if name == "" {
		return errors.New("summary activity name is required")
	}
	if fn == nil {
		return errors.New("summary activity handler is required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.summaryActivities == nil {
		e.summaryActivities = make(map[string]summaryActivityDef)
	}
	if _, dup := e.summaryActivities[name]; dup {
		return fmt.Errorf("summary activity %q already registered", name)
	}
	e.summaryActivities[name] = summaryActivityDef{handler: fn, opts: opts}
	return nil
}

// ExecuteSummarizeContextActivity folds through the registered handler and timeout.
func (w *wfCtx) ExecuteSummarizeContextActivity(ctx context.Context, call engine.SummarizeContextActivityCall) (*api.SummarizeContextOutput, error) {
	if call.Name == "" {
		return nil, errors.New("summary activity name is required")
	}
	if call.Input == nil {
		return nil, errors.New("summary activity input is required")
	}
	w.eng.mu.RLock()
	def, ok := w.eng.summaryActivities[call.Name]
	w.eng.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("summary activity %q not registered", call.Name)
	}
	actCtx, cancel := w.activityContext(ctx, call.Name, def.opts, call.Options)
	defer cancel()
	out, err := def.handler(engine.WithActivityAttempt(actCtx, 1), call.Input)
	return out, normalizeError(err)
}
