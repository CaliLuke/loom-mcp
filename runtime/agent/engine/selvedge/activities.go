package selvedge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine/internal/activity"
	sv "github.com/CaliLuke/selvedge"
)

type activityHeartbeatRecorder struct{ ctx context.Context }

type activityRegistry map[string]*sv.Activity[json.RawMessage, json.RawMessage]

// RegisterActivity installs a typed handler with the backend's existing JSON encoding.
func (e *Engine) RegisterActivity(_ context.Context, def engine.ActivityDefinition) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.registrationError(def.Name); err != nil {
		return err
	}
	handler, err := activity.New(def.Handler)
	if err != nil {
		return err
	}
	if _, exists := e.activities[def.Name]; exists {
		return fmt.Errorf("selvedge: activity %q already registered", def.Name)
	}
	opts := def.Options
	switch def.Class {
	case "hook":
		opts = applyActivityDefaults(opts, e.activityDefaults.Hook)
	case "planner":
		opts = applyActivityDefaults(opts, e.activityDefaults.Planner)
	case "tool":
		opts = applyActivityDefaults(opts, e.activityDefaults.Tool)
	}
	definition := sv.DefineActivity(def.Name, func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		info := sv.GetActivityInfo(ctx)
		ctx = engine.WithActivityContext(ctx)
		ctx = engine.WithActivityAttempt(ctx, info.Attempt)
		ctx = engine.WithActivityHeartbeatTimeout(ctx, info.Options.HeartbeatTimeout)
		ctx = engine.WithActivityHeartbeatRecorder(ctx, activityHeartbeatRecorder{ctx: ctx})
		out, err := handler.CallJSON(ctx, input)
		return out, activityError(err)
	}, sv.WithActivityDefaults[json.RawMessage, json.RawMessage](definitionActivityOptions(opts)))
	if err := sv.RegisterActivity(e.app, definition); err != nil {
		return err
	}
	e.activities[def.Name] = definition
	return nil
}

func (r activityHeartbeatRecorder) RecordHeartbeat(...any) {
	// The recorder has no error result; activity cancellation carries lost ownership.
	if err := sv.RecordHeartbeat(r.ctx); err != nil {
		return
	}
}
