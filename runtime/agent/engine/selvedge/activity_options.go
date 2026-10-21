package selvedge

import (
	"time"

	"github.com/CaliLuke/loom-mcp/v2/runtime/agent/engine"
	sv "github.com/CaliLuke/selvedge"
	"github.com/CaliLuke/selvedge/retry"
)

// ActivityDefaults provides optional timeouts for each activity class.
type ActivityDefaults struct {
	Hook, Planner, Tool ActivityTimeoutDefaults
}

// ActivityTimeoutDefaults supplies queue wait and liveness limits.
type ActivityTimeoutDefaults struct {
	QueueWait, Liveness time.Duration
}

func definitionActivityOptions(options engine.ActivityOptions) sv.ActivityOptions {
	result := invocationActivityOptions(options)
	if result.StartToCloseTimeout == 0 {
		result.StartToCloseTimeout = 2 * time.Minute
	}
	if result.RetryPolicy == nil {
		result.RetryPolicy = &retry.Policy{}
	}
	if result.RetryPolicy.InitialInterval == 0 {
		result.RetryPolicy.InitialInterval = time.Second
	}
	if result.RetryPolicy.Multiplier == 0 {
		result.RetryPolicy.Multiplier = 2
	}
	result.RetryPolicy.MaxInterval = 100 * result.RetryPolicy.InitialInterval
	return result
}

// invocationActivityOptions leaves zero fields unset so recorded definition defaults survive.
func invocationActivityOptions(options engine.ActivityOptions) sv.ActivityOptions {
	result := sv.ActivityOptions{Queue: options.Queue, StartToCloseTimeout: options.StartToCloseTimeout, ScheduleToStartTimeout: options.ScheduleToStartTimeout, HeartbeatTimeout: options.HeartbeatTimeout}
	p := options.RetryPolicy
	if p.MaxAttempts != 0 || p.InitialInterval != 0 || p.BackoffCoefficient != 0 {
		result.RetryPolicy = &retry.Policy{MaxAttempts: p.MaxAttempts, InitialInterval: p.InitialInterval, MaxInterval: 100 * p.InitialInterval, Multiplier: p.BackoffCoefficient}
	}
	return result
}

func (e *Engine) activityOptionsForClass(class string, options engine.ActivityOptions) engine.ActivityOptions {
	switch class {
	case "hook":
		return applyActivityDefaults(options, e.activityDefaults.Hook)
	case "planner":
		return applyActivityDefaults(options, e.activityDefaults.Planner)
	case "tool":
		return applyActivityDefaults(options, e.activityDefaults.Tool)
	default:
		return options
	}
}

func applyActivityDefaults(options engine.ActivityOptions, defaults ActivityTimeoutDefaults) engine.ActivityOptions {
	if options.ScheduleToStartTimeout == 0 {
		options.ScheduleToStartTimeout = defaults.QueueWait
	}
	if options.HeartbeatTimeout == 0 {
		options.HeartbeatTimeout = defaults.Liveness
	}
	return options
}
