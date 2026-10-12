package inmem

import (
	"context"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
)

// mergeActivityOptions overlays only non-zero invocation fields on registration
// defaults. Queue and retry settings are observable metadata, not scheduling.
func mergeActivityOptions(defaults, override engine.ActivityOptions) engine.ActivityOptions {
	if override.Queue != "" {
		defaults.Queue = override.Queue
	}
	if override.StartToCloseTimeout != 0 {
		defaults.StartToCloseTimeout = override.StartToCloseTimeout
	}
	if override.ScheduleToStartTimeout != 0 {
		defaults.ScheduleToStartTimeout = override.ScheduleToStartTimeout
	}
	if override.HeartbeatTimeout != 0 {
		defaults.HeartbeatTimeout = override.HeartbeatTimeout
	}
	if defaults.StartToCloseTimeout > 0 && defaults.HeartbeatTimeout > defaults.StartToCloseTimeout {
		defaults.HeartbeatTimeout = defaults.StartToCloseTimeout
	}
	if override.RetryPolicy.MaxAttempts != 0 {
		defaults.RetryPolicy.MaxAttempts = override.RetryPolicy.MaxAttempts
	}
	if override.RetryPolicy.InitialInterval != 0 {
		defaults.RetryPolicy.InitialInterval = override.RetryPolicy.InitialInterval
	}
	if override.RetryPolicy.BackoffCoefficient != 0 {
		defaults.RetryPolicy.BackoffCoefficient = override.RetryPolicy.BackoffCoefficient
	}
	return defaults
}

// activityContext observes options at dispatch and exposes heartbeat metadata.
// Only StartToCloseTimeout is enforced; the engine still executes one attempt.
func (w *wfCtx) activityContext(ctx context.Context, name string, defaults, override engine.ActivityOptions) (context.Context, context.CancelFunc) {
	opts := mergeActivityOptions(defaults, override)
	if w.eng.observeActivity != nil {
		w.eng.observeActivity(w.id, name, opts)
	}
	ctx = engine.WithActivityHeartbeatTimeout(ctx, opts.HeartbeatTimeout)
	return withOptionalTimeout(ctx, opts.StartToCloseTimeout)
}
