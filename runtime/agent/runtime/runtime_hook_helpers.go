package runtime

import (
	"context"
	"fmt"

	"github.com/CaliLuke/loom-mcp/runtime/agent/engine"
	"github.com/CaliLuke/loom-mcp/runtime/agent/hooks"
	"github.com/CaliLuke/loom-mcp/runtime/agent/prompt"
	"github.com/CaliLuke/loom-mcp/runtime/agent/rawjson"
)

// SubscriberMode classifies how a hook-bus subscriber's errors are treated.
//
//   - SubscriberCritical: errors propagate out of Bus.Publish and can fail the
//     hook activity (and therefore the agent run). Use for sinks whose
//     correctness is coupled to the canonical run record (audit/runlog).
//   - SubscriberBestEffort: errors are recorded on the logger and tracer but
//     never propagate. Use for derived projections (session metadata, memory
//     projection) and external observers (stream) that must not corrupt the
//     canonical record.
type SubscriberMode int

const (
	// SubscriberCritical subscribers fail the hook activity (and therefore the
	// run) on error.
	SubscriberCritical SubscriberMode = iota
	// SubscriberBestEffort subscribers cannot fail a run; their errors are
	// logged and recorded on the tracer only.
	SubscriberBestEffort
)

const maxHookPayloadBytes = 1_000_000

// logWarn emits a warning log and records the error in the current span if tracing is enabled.
func (r *Runtime) logWarn(ctx context.Context, msg string, err error, kv ...any) {
	fields := append([]any{}, kv...)
	if err != nil {
		fields = append(fields, "err", err)
	}
	r.logger.Warn(ctx, msg, fields...)
	if err != nil {
		span := r.tracer.Span(ctx)
		if span != nil {
			span.RecordError(err)
		}
	}
}

// publishHookErr emits a runtime hook event and returns an error on failure.
func (r *Runtime) publishHookErr(ctx context.Context, evt hooks.Event, turnID string) error {
	in, err := hooks.EncodeToHookInput(evt, turnID)
	if err != nil {
		return err
	}
	if len(in.Payload) > maxHookPayloadBytes {
		in, err = compactOversizedHookInput(evt, turnID)
		if err != nil {
			return err
		}
	}
	if wfCtx := engine.WorkflowContextFromContext(ctx); wfCtx != nil && !engine.IsActivityContext(ctx) {
		return wfCtx.PublishHook(ctx, engine.HookActivityCall{
			Name:  hookActivityName,
			Input: in,
		})
	}
	return r.hookActivity(ctx, in)
}

// compactOversizedHookInput rewrites oversized hook payloads to preserve critical metadata while dropping non-essential large blobs.
func compactOversizedHookInput(evt hooks.Event, turnID string) (*hooks.ActivityInput, error) {
	toolEvt, ok := evt.(*hooks.ToolResultReceivedEvent)
	if !ok {
		return nil, fmt.Errorf("hook payload too large for %s: cannot compact event type", string(evt.Type()))
	}
	compact := *toolEvt
	compact.Result = nil
	in, err := hooks.EncodeToHookInput(&compact, turnID)
	if err != nil {
		return nil, err
	}
	if len(in.Payload) <= maxHookPayloadBytes {
		return in, nil
	}
	compact.ServerData = nil
	in, err = hooks.EncodeToHookInput(&compact, turnID)
	if err != nil {
		return nil, err
	}
	if len(in.Payload) <= maxHookPayloadBytes {
		return in, nil
	}
	compact.ResultJSON = rawjson.Message([]byte(`{"truncated":true,"reason":"hook_payload_too_large"}`))
	if compact.ResultPreview == "" {
		compact.ResultPreview = "Result omitted from run hooks because payload exceeded limits."
	}
	in, err = hooks.EncodeToHookInput(&compact, turnID)
	if err != nil {
		return nil, err
	}
	if len(in.Payload) <= maxHookPayloadBytes {
		return in, nil
	}
	return nil, fmt.Errorf("hook payload too large for %s: %d bytes (limit %d)", string(evt.Type()), len(in.Payload), maxHookPayloadBytes)
}

// publishHook emits a runtime hook event and returns an error on failure.
func (r *Runtime) publishHook(ctx context.Context, evt hooks.Event, turnID string) error {
	return r.publishHookErr(ctx, evt, turnID)
}

// registerSubscriber installs sub on bus and returns the subscription. When
// mode is SubscriberBestEffort, sub's HandleEvent errors are logged and
// recorded on the runtime tracer but never surface back to Bus.Publish. When
// mode is SubscriberCritical, sub is installed unchanged so that its errors
// propagate out of Bus.Publish and can fail the hook activity.
func (r *Runtime) registerSubscriber(bus hooks.Bus, sub hooks.Subscriber, mode SubscriberMode) (hooks.Subscription, error) {
	if mode == SubscriberBestEffort {
		sub = r.wrapBestEffortSubscriber(sub)
	}
	return bus.Register(sub)
}

func (r *Runtime) wrapBestEffortSubscriber(sub hooks.Subscriber) hooks.Subscriber {
	return hooks.SubscriberFunc(func(ctx context.Context, event hooks.Event) error {
		if sub == nil {
			return nil
		}
		if err := sub.HandleEvent(ctx, event); err != nil {
			r.logWarn(ctx, "best-effort hook subscriber failed", err, "event", string(event.Type()))
		}
		return nil
	})
}

// onPromptRendered is the runtime-owned observer callback used by PromptRegistry.
func (r *Runtime) onPromptRendered(ctx context.Context, event prompt.RenderEvent) {
	meta, ok := promptRenderHookContextFromContext(ctx)
	if !ok {
		r.logWarn(
			ctx,
			"prompt_rendered hook skipped: missing hook context",
			fmt.Errorf("runtime: prompt_rendered missing hook context"),
			"prompt_id", event.PromptID,
			"version", event.Version,
		)
		return
	}
	hookEvent := hooks.NewPromptRenderedEvent(
		meta.RunID,
		meta.AgentID,
		meta.SessionID,
		event.PromptID,
		event.Version,
		event.Scope,
	)
	if err := r.publishHookErr(ctx, hookEvent, meta.TurnID); err != nil {
		r.logWarn(
			ctx,
			"prompt_rendered hook publish failed",
			err,
			"run_id", meta.RunID,
			"prompt_id", event.PromptID,
			"version", event.Version,
		)
	}
}
