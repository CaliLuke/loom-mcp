package selvedge

import (
	"context"
	"errors"
	"fmt"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/api"
	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	sv "github.com/CaliLuke/selvedge"
)

// activityHeartbeatRecorder reports progress without persisting user details.
type activityHeartbeatRecorder struct{ ctx context.Context }

type activityRegistry struct {
	names            map[string]struct{}
	hook             map[string]*sv.Activity[*api.HookActivityInput, struct{}]
	planner          map[string]*sv.Activity[*api.PlanActivityInput, *api.PlanActivityOutput]
	tool             map[string]*sv.Activity[*api.ToolInput, *api.ToolOutput]
	sealRecord       map[string]*sv.Activity[*api.SealRecordInput, *api.SealRecordOutput]
	finishRun        map[string]*sv.Activity[*api.FinishRunInput, *api.FinishRunOutput]
	recordContent    map[string]*sv.Activity[*api.RecordContentInput, *api.RecordContentOutput]
	summarizeContext map[string]*sv.Activity[*api.SummarizeContextInput, *api.SummarizeContextOutput]
	recall           map[string]*sv.Activity[*api.RecallActivityInput, *api.RecallActivityOutput]
	childDispatch    map[string]*sv.Activity[*api.ChildDispatchInput, *api.ChildDispatchOutput]
}

// RegisterHookActivity registers hook delivery as a typed activity.
func (e *Engine) RegisterHookActivity(_ context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.HookActivityInput) error) error {
	if fn == nil {
		return errors.New("selvedge: activity handler is required")
	}
	return registerActivity(e, e.activities.hook, name, applyActivityDefaults(opts, e.activityDefaults.Hook), func(ctx context.Context, in *api.HookActivityInput) (struct{}, error) {
		return struct{}{}, fn(ctx, in)
	})
}

// RegisterPlannerActivity registers the typed planner handler.
func (e *Engine) RegisterPlannerActivity(_ context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.PlanActivityInput) (*api.PlanActivityOutput, error)) error {
	return registerActivity(e, e.activities.planner, name, applyActivityDefaults(opts, e.activityDefaults.Planner), fn)
}

// RegisterExecuteToolActivity registers the typed tool handler.
func (e *Engine) RegisterExecuteToolActivity(_ context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.ToolInput) (*api.ToolOutput, error)) error {
	return registerActivity(e, e.activities.tool, name, applyActivityDefaults(opts, e.activityDefaults.Tool), fn)
}

// RegisterSealRecordActivity registers the typed sealRecord handler.
func (e *Engine) RegisterSealRecordActivity(_ context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.SealRecordInput) (*api.SealRecordOutput, error)) error {
	return registerActivity(e, e.activities.sealRecord, name, applyActivityDefaults(opts, e.activityDefaults.Hook), fn)
}

// RegisterFinishRunActivity registers the typed finishRun handler.
func (e *Engine) RegisterFinishRunActivity(_ context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.FinishRunInput) (*api.FinishRunOutput, error)) error {
	return registerActivity(e, e.activities.finishRun, name, applyActivityDefaults(opts, e.activityDefaults.Hook), fn)
}

// RegisterRecordContentActivity registers the typed recordContent handler.
func (e *Engine) RegisterRecordContentActivity(_ context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.RecordContentInput) (*api.RecordContentOutput, error)) error {
	return registerActivity(e, e.activities.recordContent, name, applyActivityDefaults(opts, e.activityDefaults.Hook), fn)
}

// RegisterSummarizeContextActivity registers the typed summarizeContext handler.
func (e *Engine) RegisterSummarizeContextActivity(_ context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.SummarizeContextInput) (*api.SummarizeContextOutput, error)) error {
	return registerActivity(e, e.activities.summarizeContext, name, applyActivityDefaults(opts, e.activityDefaults.Planner), fn)
}

// RegisterRecallActivity registers the typed recall handler.
func (e *Engine) RegisterRecallActivity(_ context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.RecallActivityInput) (*api.RecallActivityOutput, error)) error {
	return registerActivity(e, e.activities.recall, name, applyActivityDefaults(opts, e.activityDefaults.Hook), fn)
}

// RegisterChildDispatchActivity registers the typed childDispatch handler.
func (e *Engine) RegisterChildDispatchActivity(_ context.Context, name string, opts engine.ActivityOptions, fn func(context.Context, *api.ChildDispatchInput) (*api.ChildDispatchOutput, error)) error {
	return registerActivity(e, e.activities.childDispatch, name, applyActivityDefaults(opts, e.activityDefaults.Hook), fn)
}

func newActivityRegistry() activityRegistry {
	return activityRegistry{names: make(map[string]struct{}), hook: make(map[string]*sv.Activity[*api.HookActivityInput, struct{}]),
		planner:          make(map[string]*sv.Activity[*api.PlanActivityInput, *api.PlanActivityOutput]),
		tool:             make(map[string]*sv.Activity[*api.ToolInput, *api.ToolOutput]),
		sealRecord:       make(map[string]*sv.Activity[*api.SealRecordInput, *api.SealRecordOutput]),
		finishRun:        make(map[string]*sv.Activity[*api.FinishRunInput, *api.FinishRunOutput]),
		recordContent:    make(map[string]*sv.Activity[*api.RecordContentInput, *api.RecordContentOutput]),
		summarizeContext: make(map[string]*sv.Activity[*api.SummarizeContextInput, *api.SummarizeContextOutput]),
		recall:           make(map[string]*sv.Activity[*api.RecallActivityInput, *api.RecallActivityOutput]),
		childDispatch:    make(map[string]*sv.Activity[*api.ChildDispatchInput, *api.ChildDispatchOutput]),
	}
}

func registerActivity[I, O any](e *Engine, registry map[string]*sv.Activity[I, O], name string, opts engine.ActivityOptions, fn func(context.Context, I) (O, error)) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.registrationError(name); err != nil {
		return err
	}
	if fn == nil {
		return errors.New("selvedge: activity handler is required")
	}
	if _, exists := e.activities.names[name]; exists {
		return fmt.Errorf("selvedge: activity %q already registered", name)
	}
	definition := defineActivity(name, opts, fn)
	if err := sv.RegisterActivity(e.app, definition); err != nil {
		return err
	}
	registry[name] = definition
	e.activities.names[name] = struct{}{}
	return nil
}

func defineActivity[I, O any](name string, opts engine.ActivityOptions, fn func(context.Context, I) (O, error)) *sv.Activity[I, O] {
	return sv.DefineActivity(name, func(ctx context.Context, input I) (O, error) {
		info := sv.GetActivityInfo(ctx)
		ctx = engine.WithActivityContext(ctx)
		ctx = engine.WithActivityAttempt(ctx, info.Attempt)
		ctx = engine.WithActivityHeartbeatTimeout(ctx, info.Options.HeartbeatTimeout)
		ctx = engine.WithActivityHeartbeatRecorder(ctx, activityHeartbeatRecorder{ctx: ctx})
		out, err := fn(ctx, input)
		return out, activityError(err)
	}, sv.WithActivityDefaults[I, O](definitionActivityOptions(opts)))
}

func (r activityHeartbeatRecorder) RecordHeartbeat(...any) {
	// The engine recorder has no error result; activity cancellation carries loss of ownership.
	if err := sv.RecordHeartbeat(r.ctx); err != nil {
		return
	}
}
