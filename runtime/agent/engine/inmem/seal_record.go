package inmem

import (
	"context"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
)

type sealActivityDef struct {
	handler func(context.Context, *api.SealRecordInput) (*api.SealRecordOutput, error)
	opts    engine.ActivityOptions
}

// RegisterSealRecordActivity registers terminal transcript sealing.
func (e *eng) RegisterSealRecordActivity(_ context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.SealRecordInput) (*api.SealRecordOutput, error)) error {
	if name == "" {
		return errors.New("seal activity name is required")
	}
	if fn == nil {
		return errors.New("seal activity handler is required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sealActivities == nil {
		e.sealActivities = make(map[string]sealActivityDef)
	}
	if _, dup := e.sealActivities[name]; dup {
		return fmt.Errorf("seal activity %q already registered", name)
	}
	e.sealActivities[name] = sealActivityDef{handler: fn, opts: opts}
	return nil
}

// ExecuteSealRecordActivity seals through the registered handler and timeout.
func (w *wfCtx) ExecuteSealRecordActivity(ctx context.Context, call engine.SealRecordActivityCall) (*api.SealRecordOutput, error) {
	if call.Name == "" {
		return nil, errors.New("seal activity name is required")
	}
	if call.Input == nil {
		return nil, errors.New("seal activity input is required")
	}
	w.eng.mu.RLock()
	def, ok := w.eng.sealActivities[call.Name]
	w.eng.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("seal activity %q not registered", call.Name)
	}
	actCtx, cancel := w.activityContext(ctx, call.Name, def.opts, call.Options)
	defer cancel()
	out, err := def.handler(actCtx, call.Input)
	return out, normalizeError(err)
}
